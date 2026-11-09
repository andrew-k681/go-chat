package hub

import "time"

type EventKind int

const (
	RoomCreated EventKind = iota + 1
	PeerJoined
	PeerLeft
	RoomExpired
	JoinRejectedFull
)

// Event describes something that happened to a room. RoomID is the raw room
// id (a credential): sinks must hash it before it leaves the process.
type Event struct {
	Kind   EventKind
	RoomID string
	PeerID string // PeerJoined, PeerLeft
	Peers  int    // peers in the room after the event
	At     time.Time
}
