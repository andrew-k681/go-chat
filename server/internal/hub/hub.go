// Package hub keeps the in-memory rooms and relays signaling messages between
// peers of the same room. It knows nothing about WebSockets: each Peer has an
// outbound queue that the connection's writer goroutine drains.
package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"
)

// Error texts are sent to clients verbatim (see PROTOCOL.md).
var (
	ErrRoomNotFound = errors.New("room not found")
	ErrRoomFull     = errors.New("room full")
	ErrTooManyRooms = errors.New("too many rooms")
	ErrIPRoomLimit  = errors.New("too many rooms from this address")
	ErrShuttingDown = errors.New("server shutting down")
)

// peerQueueLen bounds memory per peer; a full queue drops the peer.
const peerQueueLen = 64

type Options struct {
	MaxPeers      int           // peers per room
	MaxRooms      int           // rooms in total
	MaxRoomsPerIP int           // live rooms created by one client address
	RoomTTL       time.Duration // how long an empty room survives

	// OnEvent, if set, receives room lifecycle events. It runs under the hub
	// lock, which keeps events in the order they happened, so it must be fast
	// and must never block.
	OnEvent func(Event)
}

type Hub struct {
	opts Options
	now  func() time.Time

	mu     sync.Mutex
	rooms  map[string]*room
	perIP  map[string]int
	closed bool
}

type room struct {
	peers      []*Peer // in join order
	creatorIP  string
	emptySince time.Time // zero while someone is in the room
}

func New(opts Options) *Hub {
	return &Hub{
		opts:  opts,
		now:   time.Now,
		rooms: make(map[string]*room),
		perIP: make(map[string]int),
	}
}

// NewRoomID returns 128 random bits, base64url-encoded (22 chars).
func NewRoomID() string {
	b := make([]byte, 16)
	rand.Read(b) // never returns an error; crashes the program if the OS RNG fails
	return base64.RawURLEncoding.EncodeToString(b)
}

// CreateRoom registers a new empty room created by the client at ip.
func (h *Hub) CreateRoom(ip string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return "", ErrShuttingDown
	case len(h.rooms) >= h.opts.MaxRooms:
		return "", ErrTooManyRooms
	case h.perIP[ip] >= h.opts.MaxRoomsPerIP:
		return "", ErrIPRoomLimit
	}
	id := NewRoomID()
	h.rooms[id] = &room{creatorIP: ip, emptySince: h.now()}
	h.perIP[ip]++
	h.emit(Event{Kind: RoomCreated, RoomID: id})
	return id, nil
}

// PeerCount reports how many peers are in a room and whether it exists.
func (h *Hub) PeerCount(id string) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[id]
	if !ok {
		return 0, false
	}
	return len(r.peers), true
}

// Join adds a peer to the room. Existing peers get peer-joined before the
// newcomer gets welcome, so nobody receives an offer from an unknown peer.
func (h *Hub) Join(roomID, name string) (*Peer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrShuttingDown
	}
	r, ok := h.rooms[roomID]
	if !ok {
		return nil, ErrRoomNotFound
	}
	if len(r.peers) >= h.opts.MaxPeers {
		h.emit(Event{Kind: JoinRejectedFull, RoomID: roomID, Peers: len(r.peers)})
		return nil, ErrRoomFull
	}

	p := newPeer(rand.Text(), name)
	joined := mustJSON(peerJoinedMsg{Type: "peer-joined", Peer: p.info()})
	others := make([]PeerInfo, 0, len(r.peers))
	for _, o := range r.peers {
		o.enqueue(joined)
		others = append(others, o.info())
	}
	r.peers = append(r.peers, p)
	r.emptySince = time.Time{}
	p.enqueue(mustJSON(welcomeMsg{Type: "welcome", ID: p.ID, Peers: others}))
	h.emit(Event{Kind: PeerJoined, RoomID: roomID, PeerID: p.ID, Peers: len(r.peers)})
	return p, nil
}

// Leave removes the peer and tells the rest of the room. Safe to call twice.
func (h *Hub) Leave(roomID string, p *Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[roomID]
	if !ok {
		return
	}
	i := slices.Index(r.peers, p)
	if i < 0 {
		return
	}
	r.peers = slices.Delete(r.peers, i, i+1)
	left := mustJSON(peerLeftMsg{Type: "peer-left", ID: p.ID})
	for _, o := range r.peers {
		o.enqueue(left)
	}
	if len(r.peers) == 0 {
		r.emptySince = h.now()
	}
	h.emit(Event{Kind: PeerLeft, RoomID: roomID, PeerID: p.ID, Peers: len(r.peers)})
}

// Relay forwards opaque signaling data to peer `to` in the same room. The
// sender id comes from the server, never from the client. Unknown targets
// (e.g. a peer that just left) are dropped silently.
func (h *Hub) Relay(roomID string, from *Peer, to string, data json.RawMessage) {
	if to == from.ID {
		return
	}
	msg := mustJSON(signalMsg{Type: "signal", From: from.ID, Data: data})
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[roomID]
	if !ok || !slices.Contains(r.peers, from) {
		return
	}
	for _, o := range r.peers {
		if o.ID == to {
			o.enqueue(msg)
			return
		}
	}
}

// Sweep deletes rooms that have been empty for longer than RoomTTL.
func (h *Hub) Sweep() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := h.now().Add(-h.opts.RoomTTL)
	for id, r := range h.rooms {
		if len(r.peers) == 0 && r.emptySince.Before(cutoff) {
			delete(h.rooms, id)
			h.emit(Event{Kind: RoomExpired, RoomID: id})
			if h.perIP[r.creatorIP]--; h.perIP[r.creatorIP] <= 0 {
				delete(h.perIP, r.creatorIP)
			}
		}
	}
}

// Run sweeps periodically until ctx is done.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(min(h.opts.RoomTTL/4, time.Minute))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Sweep()
		}
	}
}

// Close disconnects every peer and refuses new rooms and joins.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, r := range h.rooms {
		for _, p := range r.peers {
			p.kick()
		}
	}
}

// emit must be called with h.mu held.
func (h *Hub) emit(ev Event) {
	if h.opts.OnEvent != nil {
		ev.At = h.now()
		h.opts.OnEvent(ev)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only fixed structs are marshalled here
	}
	return b
}
