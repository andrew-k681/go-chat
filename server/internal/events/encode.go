// Package events turns hub events into protobuf RoomEvents and moves them
// through Kafka (producer here, consumer in internal/stats).
package events

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"

	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/andrew-k681/go-chat/server/gen/gochat/events/v1"
	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/hub"
)

// ContentType goes in the record header so consumers know how to decode the value.
const ContentType = "application/x-protobuf; messageType=gochat.events.v1.RoomEvent"

type Encoder struct {
	key []byte
}

func NewEncoder(key config.Secret) *Encoder {
	return &Encoder{key: []byte(key.Reveal())}
}

// RoomRef is a stable pseudonym for a room. It can't be turned back into the
// room id (the invite credential) without EVENTS_KEY.
func (e *Encoder) RoomRef(roomID string) string {
	mac := hmac.New(sha256.New, e.key)
	mac.Write([]byte(roomID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Encode returns nil for kinds this version doesn't know.
func (e *Encoder) Encode(ev hub.Event) *eventsv1.RoomEvent {
	out := &eventsv1.RoomEvent{
		EventId:    rand.Text(),
		RoomRef:    e.RoomRef(ev.RoomID),
		OccurredAt: timestamppb.New(ev.At),
	}
	peers := uint32(max(ev.Peers, 0))
	switch ev.Kind {
	case hub.RoomCreated:
		out.Kind = &eventsv1.RoomEvent_RoomCreated{RoomCreated: &eventsv1.RoomCreated{}}
	case hub.PeerJoined:
		out.Kind = &eventsv1.RoomEvent_PeerJoined{PeerJoined: &eventsv1.PeerJoined{PeerId: ev.PeerID, PeersNow: peers}}
	case hub.PeerLeft:
		out.Kind = &eventsv1.RoomEvent_PeerLeft{PeerLeft: &eventsv1.PeerLeft{PeerId: ev.PeerID, PeersNow: peers}}
	case hub.RoomExpired:
		out.Kind = &eventsv1.RoomEvent_RoomExpired{RoomExpired: &eventsv1.RoomExpired{}}
	case hub.JoinRejectedFull:
		out.Kind = &eventsv1.RoomEvent_JoinRejected{JoinRejected: &eventsv1.JoinRejected{
			Reason: eventsv1.JoinRejected_REASON_ROOM_FULL,
		}}
	default:
		return nil
	}
	return out
}
