package hub

import (
	"encoding/json"
	"sync"
)

// Peer is one participant. The hub writes into its outbound queue; the
// connection's writer goroutine drains it, which keeps per-peer order.
type Peer struct {
	ID   string
	Name string

	out  chan []byte
	gone chan struct{}
	once sync.Once
}

func newPeer(id, name string) *Peer {
	return &Peer{ID: id, Name: name, out: make(chan []byte, peerQueueLen), gone: make(chan struct{})}
}

// Out yields encoded server messages for this peer, in order.
func (p *Peer) Out() <-chan []byte { return p.out }

// Gone is closed when the hub drops the peer (slow consumer or shutdown).
// The connection should close; its reader then calls Leave.
func (p *Peer) Gone() <-chan struct{} { return p.gone }

// enqueue never blocks the hub: a peer that can't keep up is dropped.
func (p *Peer) enqueue(msg []byte) {
	select {
	case p.out <- msg:
	default:
		p.kick()
	}
}

func (p *Peer) kick() { p.once.Do(func() { close(p.gone) }) }

func (p *Peer) info() PeerInfo { return PeerInfo{ID: p.ID, Name: p.Name} }

// Wire messages, server → client (see PROTOCOL.md).

type PeerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type welcomeMsg struct {
	Type  string     `json:"type"`
	ID    string     `json:"id"`
	Peers []PeerInfo `json:"peers"`
}

type peerJoinedMsg struct {
	Type string   `json:"type"`
	Peer PeerInfo `json:"peer"`
}

type peerLeftMsg struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type signalMsg struct {
	Type string          `json:"type"`
	From string          `json:"from"`
	Data json.RawMessage `json:"data"`
}

// ErrorMessage encodes {"type":"error","message":...}.
func ErrorMessage(text string) []byte {
	return mustJSON(struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}{"error", text})
}
