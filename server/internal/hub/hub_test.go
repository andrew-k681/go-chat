package hub

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"
)

func newHub() *Hub {
	return New(Options{MaxPeers: 3, MaxRooms: 5, MaxRoomsPerIP: 2, RoomTTL: 10 * time.Minute})
}

// next pops one queued message and decodes it into a map.
func next(t *testing.T, p *Peer) map[string]any {
	t.Helper()
	select {
	case b := <-p.Out():
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	default:
		t.Fatalf("no message queued for %s", p.Name)
		return nil
	}
}

func TestRoomIDFormat(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	seen := map[string]bool{}
	for range 100 {
		id := NewRoomID()
		if !re.MatchString(id) || seen[id] {
			t.Fatalf("bad or duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestJoinOrderingAndRelay(t *testing.T) {
	h := newHub()
	id, _ := h.CreateRoom("1.1.1.1")

	alice, err := h.Join(id, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if w := next(t, alice); w["type"] != "welcome" || len(w["peers"].([]any)) != 0 {
		t.Fatalf("alice welcome = %v", w)
	}

	bob, _ := h.Join(id, "Bob")
	if m := next(t, alice); m["type"] != "peer-joined" || m["peer"].(map[string]any)["name"] != "Bob" {
		t.Fatalf("alice got %v", m)
	}
	w := next(t, bob)
	if w["type"] != "welcome" || w["id"] != bob.ID {
		t.Fatalf("bob welcome = %v", w)
	}
	if peers := w["peers"].([]any); len(peers) != 1 || peers[0].(map[string]any)["id"] != alice.ID {
		t.Fatalf("bob peers = %v", peers)
	}

	h.Relay(id, bob, alice.ID, json.RawMessage(`{"description":{"type":"offer","sdp":"x"}}`))
	if m := next(t, alice); m["type"] != "signal" || m["from"] != bob.ID {
		t.Fatalf("relay = %v", m)
	}

	h.Leave(id, bob)
	if m := next(t, alice); m["type"] != "peer-left" || m["id"] != bob.ID {
		t.Fatalf("leave = %v", m)
	}
	h.Leave(id, bob) // idempotent
	if n, _ := h.PeerCount(id); n != 1 {
		t.Errorf("PeerCount = %d", n)
	}
}

func TestRelayStaysInRoom(t *testing.T) {
	h := newHub()
	r1, _ := h.CreateRoom("ip")
	r2, _ := h.CreateRoom("ip")
	a, _ := h.Join(r1, "A")
	b, _ := h.Join(r2, "B")
	next(t, a)
	next(t, b)

	h.Relay(r1, a, b.ID, json.RawMessage(`{}`)) // b is in another room
	h.Relay(r2, a, b.ID, json.RawMessage(`{}`)) // a lies about its room
	select {
	case m := <-b.Out():
		t.Fatalf("cross-room relay leaked: %s", m)
	default:
	}
}

func TestLimits(t *testing.T) {
	h := newHub()
	id, _ := h.CreateRoom("ip")
	for range 3 {
		if _, err := h.Join(id, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Join(id, "x"); !errors.Is(err, ErrRoomFull) {
		t.Errorf("4th join: %v", err)
	}
	if _, err := h.Join("nope", "x"); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("unknown room: %v", err)
	}

	h.CreateRoom("ip")
	if _, err := h.CreateRoom("ip"); !errors.Is(err, ErrIPRoomLimit) {
		t.Errorf("per-IP limit: %v", err)
	}
	h.CreateRoom("a")
	h.CreateRoom("b")
	h.CreateRoom("c")
	if _, err := h.CreateRoom("d"); !errors.Is(err, ErrTooManyRooms) {
		t.Errorf("global limit: %v", err)
	}
}

func TestSlowPeerIsDropped(t *testing.T) {
	h := newHub()
	id, _ := h.CreateRoom("ip")
	a, _ := h.Join(id, "A")
	b, _ := h.Join(id, "B")
	for range peerQueueLen + 1 {
		h.Relay(id, b, a.ID, json.RawMessage(`{}`))
	}
	select {
	case <-a.Gone():
	default:
		t.Fatal("slow peer should be kicked")
	}
}

func TestSweep(t *testing.T) {
	h := newHub()
	now := time.Now()
	h.now = func() time.Time { return now }

	id, _ := h.CreateRoom("ip")
	p, _ := h.Join(id, "A")
	now = now.Add(time.Hour)
	h.Sweep()
	if _, ok := h.PeerCount(id); !ok {
		t.Fatal("occupied room must survive")
	}

	h.Leave(id, p)
	now = now.Add(9 * time.Minute)
	h.Sweep()
	if _, ok := h.PeerCount(id); !ok {
		t.Fatal("room empty for less than TTL must survive")
	}
	now = now.Add(2 * time.Minute)
	h.Sweep()
	if _, ok := h.PeerCount(id); ok {
		t.Fatal("room should be swept")
	}
	if len(h.perIP) != 0 {
		t.Errorf("perIP not released: %v", h.perIP)
	}
}

func TestClose(t *testing.T) {
	h := newHub()
	id, _ := h.CreateRoom("ip")
	p, _ := h.Join(id, "A")
	h.Close()
	select {
	case <-p.Gone():
	default:
		t.Fatal("peer not kicked on Close")
	}
	if _, err := h.CreateRoom("ip"); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("CreateRoom after Close: %v", err)
	}
}

func TestEvents(t *testing.T) {
	var got []Event
	h := New(Options{MaxPeers: 1, MaxRooms: 5, MaxRoomsPerIP: 5, RoomTTL: time.Minute,
		OnEvent: func(ev Event) { got = append(got, ev) }})

	id, _ := h.CreateRoom("ip")
	p, _ := h.Join(id, "A")
	h.Join(id, "B") // full
	h.Leave(id, p)
	h.now = func() time.Time { return time.Now().Add(time.Hour) }
	h.Sweep()

	want := []struct {
		kind  EventKind
		peer  string
		peers int
	}{
		{RoomCreated, "", 0}, {PeerJoined, p.ID, 1}, {JoinRejectedFull, "", 1},
		{PeerLeft, p.ID, 0}, {RoomExpired, "", 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events: %+v", len(got), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.PeerID != w.peer || g.Peers != w.peers || g.RoomID != id || g.At.IsZero() {
			t.Errorf("event %d = %+v, want %+v", i, g, w)
		}
	}
}
