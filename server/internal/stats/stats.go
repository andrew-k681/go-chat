// Package stats folds RoomEvents into aggregate numbers. It is the consumer
// side of internal/events: state is rebuilt from the topic, never stored.
package stats

import (
	"sync"
	"time"

	eventsv1 "github.com/andrew-k681/go-chat/server/gen/gochat/events/v1"
)

const keepDays = 30

// Snapshot is what GET /stats returns. Aggregates only: no room refs or peer ids.
type Snapshot struct {
	RoomsCreated      int64            `json:"rooms_created"`
	RoomsExpired      int64            `json:"rooms_expired"`
	RoomsLive         int64            `json:"rooms_live"`
	Joins             int64            `json:"joins"`
	RejectedFull      int64            `json:"rejected_room_full"`
	PeakPeersInRoom   uint32           `json:"peak_peers_in_room"`
	Sessions          int64            `json:"sessions_completed"`
	AvgSessionSeconds float64          `json:"avg_session_seconds"`
	OpenSessions      int              `json:"open_sessions"`
	RoomsPerDay       map[string]int64 `json:"rooms_per_day"` // UTC date → rooms created
	UnknownEvents     int64            `json:"unknown_events"`
	DecodeErrors      int64            `json:"decode_errors"`
	Duplicates        int64            `json:"duplicates"`
}

type Aggregator struct {
	mu   sync.Mutex
	s    Snapshot
	open map[string]session // peer id → open session
	// seen holds recent event ids to drop duplicates; reset when it gets big.
	seen        map[string]struct{}
	sessionSecs float64
}

type session struct {
	room   string
	joined time.Time
}

func NewAggregator() *Aggregator {
	return &Aggregator{
		s:    Snapshot{RoomsPerDay: map[string]int64{}},
		open: map[string]session{},
		seen: map[string]struct{}{},
	}
}

// Apply folds one event in. Events of one room arrive in order (same partition).
func (a *Aggregator) Apply(ev *eventsv1.RoomEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id := ev.GetEventId(); id != "" {
		if _, dup := a.seen[id]; dup {
			a.s.Duplicates++
			return
		}
		if len(a.seen) >= 100_000 {
			clear(a.seen)
		}
		a.seen[id] = struct{}{}
	}

	at := ev.GetOccurredAt().AsTime()
	switch k := ev.GetKind().(type) {
	case *eventsv1.RoomEvent_RoomCreated:
		a.s.RoomsCreated++
		a.s.RoomsPerDay[at.UTC().Format(time.DateOnly)]++
		a.trimDays(at)
	case *eventsv1.RoomEvent_PeerJoined:
		a.s.Joins++
		a.s.PeakPeersInRoom = max(a.s.PeakPeersInRoom, k.PeerJoined.GetPeersNow())
		a.open[k.PeerJoined.GetPeerId()] = session{room: ev.GetRoomRef(), joined: at}
	case *eventsv1.RoomEvent_PeerLeft:
		if s, ok := a.open[k.PeerLeft.GetPeerId()]; ok {
			delete(a.open, k.PeerLeft.GetPeerId())
			a.s.Sessions++
			a.sessionSecs += at.Sub(s.joined).Seconds()
		}
	case *eventsv1.RoomEvent_RoomExpired:
		a.s.RoomsExpired++
		// A crashed server never sends PeerLeft; don't keep those sessions forever.
		for id, s := range a.open {
			if s.room == ev.GetRoomRef() {
				delete(a.open, id)
			}
		}
	case *eventsv1.RoomEvent_JoinRejected:
		if k.JoinRejected.GetReason() == eventsv1.JoinRejected_REASON_ROOM_FULL {
			a.s.RejectedFull++
		}
	default:
		// A newer producer added a kind we don't know yet. Protobuf still
		// decoded the common fields; count it and move on.
		a.s.UnknownEvents++
	}
}

// DecodeError counts a record whose value wasn't a valid RoomEvent.
func (a *Aggregator) DecodeError() {
	a.mu.Lock()
	a.s.DecodeErrors++
	a.mu.Unlock()
}

func (a *Aggregator) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.s
	out.RoomsLive = max(out.RoomsCreated-out.RoomsExpired, 0)
	out.OpenSessions = len(a.open)
	if out.Sessions > 0 {
		out.AvgSessionSeconds = a.sessionSecs / float64(out.Sessions)
	}
	out.RoomsPerDay = make(map[string]int64, len(a.s.RoomsPerDay))
	for d, n := range a.s.RoomsPerDay {
		out.RoomsPerDay[d] = n
	}
	return out
}

func (a *Aggregator) trimDays(latest time.Time) {
	cutoff := latest.UTC().AddDate(0, 0, -keepDays).Format(time.DateOnly)
	for d := range a.s.RoomsPerDay {
		if d < cutoff {
			delete(a.s.RoomsPerDay, d)
		}
	}
}
