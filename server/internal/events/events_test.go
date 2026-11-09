package events

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/andrew-k681/go-chat/server/gen/gochat/events/v1"
	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/hub"
)

var key = config.Secret(strings.Repeat("k", 32))

func TestEncodeHidesRoomID(t *testing.T) {
	e := NewEncoder(key)
	const roomID = "tbhLSNWkkB5BOVbo3mHSXw"
	at := time.Unix(1_700_000_000, 0)
	msg := e.Encode(hub.Event{Kind: hub.PeerJoined, RoomID: roomID, PeerID: "p1", Peers: 2, At: at})

	b, _ := proto.Marshal(msg)
	if strings.Contains(string(b), roomID) {
		t.Fatal("room id leaked into the event")
	}
	if msg.GetRoomRef() != e.RoomRef(roomID) || msg.GetRoomRef() == NewEncoder(config.Secret(strings.Repeat("x", 32))).RoomRef(roomID) {
		t.Error("room_ref must be stable per key and differ across keys")
	}
	if j := msg.GetPeerJoined(); j.GetPeerId() != "p1" || j.GetPeersNow() != 2 {
		t.Errorf("peer_joined = %v", j)
	}
	if !msg.GetOccurredAt().AsTime().Equal(at) || msg.GetEventId() == "" {
		t.Errorf("envelope = %v", msg)
	}
	if e.Encode(hub.Event{Kind: 99}) != nil {
		t.Error("unknown kind should encode to nil")
	}
}

// TestProducerToKafka runs the producer against an in-memory Kafka cluster.
func TestProducerToKafka(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(3, "room-events"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	k := config.Kafka{Brokers: cluster.ListenAddrs(), Topic: "room-events"}

	p, err := NewProducer(k, key, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.Emit(hub.Event{Kind: hub.RoomCreated, RoomID: "roomAAAAAAAA", At: now})
	p.Emit(hub.Event{Kind: hub.PeerJoined, RoomID: "roomAAAAAAAA", PeerID: "p1", Peers: 1, At: now})
	p.Emit(hub.Event{Kind: hub.PeerLeft, RoomID: "roomAAAAAAAA", PeerID: "p1", At: now})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Close(ctx)
	if p.Dropped() != 0 {
		t.Fatalf("dropped %d", p.Dropped())
	}

	c, err := kgo.NewClient(append(ClientOpts(k), kgo.ConsumeTopics(k.Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var got []*eventsv1.RoomEvent
	for len(got) < 3 {
		fetches := c.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			t.Fatalf("got %d of 3 records", len(got))
		}
		fetches.EachRecord(func(r *kgo.Record) {
			if string(r.Headers[0].Value) != ContentType {
				t.Errorf("header = %v", r.Headers)
			}
			var ev eventsv1.RoomEvent
			if err := proto.Unmarshal(r.Value, &ev); err != nil {
				t.Fatal(err)
			}
			if string(r.Key) != ev.GetRoomRef() {
				t.Error("record key must be room_ref")
			}
			got = append(got, &ev)
		})
	}
	// Same key → same partition → produce order is kept.
	if got[0].GetRoomCreated() == nil || got[1].GetPeerJoined() == nil || got[2].GetPeerLeft() == nil {
		t.Errorf("order = %v", got)
	}
}
