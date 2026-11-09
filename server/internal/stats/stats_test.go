package stats

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/andrew-k681/go-chat/server/gen/gochat/events/v1"
	"github.com/andrew-k681/go-chat/server/internal/config"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func ev(id, room string, at time.Time, kind any) *eventsv1.RoomEvent {
	e := &eventsv1.RoomEvent{EventId: id, RoomRef: room, OccurredAt: timestamppb.New(at)}
	switch k := kind.(type) {
	case *eventsv1.RoomCreated:
		e.Kind = &eventsv1.RoomEvent_RoomCreated{RoomCreated: k}
	case *eventsv1.PeerJoined:
		e.Kind = &eventsv1.RoomEvent_PeerJoined{PeerJoined: k}
	case *eventsv1.PeerLeft:
		e.Kind = &eventsv1.RoomEvent_PeerLeft{PeerLeft: k}
	case *eventsv1.RoomExpired:
		e.Kind = &eventsv1.RoomEvent_RoomExpired{RoomExpired: k}
	case *eventsv1.JoinRejected:
		e.Kind = &eventsv1.RoomEvent_JoinRejected{JoinRejected: k}
	}
	return e
}

func sample() []*eventsv1.RoomEvent {
	return []*eventsv1.RoomEvent{
		ev("1", "r1", t0, &eventsv1.RoomCreated{}),
		ev("2", "r1", t0, &eventsv1.PeerJoined{PeerId: "a", PeersNow: 1}),
		ev("3", "r1", t0.Add(time.Minute), &eventsv1.PeerJoined{PeerId: "b", PeersNow: 2}),
		ev("4", "r1", t0.Add(time.Minute), &eventsv1.JoinRejected{Reason: eventsv1.JoinRejected_REASON_ROOM_FULL}),
		ev("5", "r1", t0.Add(2*time.Minute), &eventsv1.PeerLeft{PeerId: "a", PeersNow: 1}),
		ev("6", "r1", t0.Add(5*time.Minute), &eventsv1.PeerLeft{PeerId: "b"}),
		ev("7", "r2", t0.Add(24*time.Hour), &eventsv1.RoomCreated{}),
		ev("8", "r2", t0.Add(24*time.Hour), &eventsv1.PeerJoined{PeerId: "c", PeersNow: 1}),
		ev("9", "r1", t0.Add(time.Hour), &eventsv1.RoomExpired{}),
	}
}

func TestAggregator(t *testing.T) {
	a := NewAggregator()
	for _, e := range sample() {
		a.Apply(e)
	}
	a.Apply(sample()[1])                                       // duplicate delivery
	a.Apply(&eventsv1.RoomEvent{EventId: "10", RoomRef: "r3"}) // kind from a newer schema
	s := a.Snapshot()

	if s.RoomsCreated != 2 || s.RoomsExpired != 1 || s.RoomsLive != 1 {
		t.Errorf("rooms: %+v", s)
	}
	if s.Joins != 3 || s.RejectedFull != 1 || s.PeakPeersInRoom != 2 {
		t.Errorf("joins: %+v", s)
	}
	// a: 2 min, b: 4 min → avg 180 s. c is still in a call.
	if s.Sessions != 2 || s.AvgSessionSeconds != 180 || s.OpenSessions != 1 {
		t.Errorf("sessions: %+v", s)
	}
	if s.RoomsPerDay["2026-10-26"] != 1 || s.RoomsPerDay["2026-10-27"] != 1 {
		t.Errorf("per day: %v", s.RoomsPerDay)
	}
	if s.Duplicates != 1 || s.UnknownEvents != 1 {
		t.Errorf("dup/unknown: %+v", s)
	}
}

func TestExpiredRoomClosesStaleSessions(t *testing.T) {
	a := NewAggregator()
	a.Apply(ev("1", "r1", t0, &eventsv1.PeerJoined{PeerId: "a", PeersNow: 1})) // server crashed, no PeerLeft
	a.Apply(ev("2", "r1", t0.Add(time.Hour), &eventsv1.RoomExpired{}))
	if s := a.Snapshot(); s.OpenSessions != 0 {
		t.Errorf("open sessions = %d", s.OpenSessions)
	}
}

func TestHandlerAuth(t *testing.T) {
	token := config.Secret(strings.Repeat("t", 32))
	h := Handler(NewAggregator(), token)
	for _, tc := range []struct {
		auth string
		want int
	}{
		{"", 401}, {"Bearer wrong", 401}, {token.Reveal(), 401}, {"Bearer " + token.Reveal(), 200},
	} {
		req := httptest.NewRequest("GET", "/stats", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("auth %q: %d, want %d", tc.auth, rec.Code, tc.want)
		}
	}
}

// TestConsumeReplays produces raw records (one of them garbage) and checks
// that a fresh consumer group rebuilds the stats from the start of the topic.
func TestConsumeReplays(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(3, "room-events"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	cfg := config.Stats{Group: "stats-test", Kafka: config.Kafka{Brokers: cluster.ListenAddrs(), Topic: "room-events"}}

	prod, err := kgo.NewClient(kgo.SeedBrokers(cfg.Kafka.Brokers...), kgo.DefaultProduceTopic("room-events"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var recs []*kgo.Record
	for _, e := range sample() {
		b, _ := proto.Marshal(e)
		recs = append(recs, &kgo.Record{Key: []byte(e.GetRoomRef()), Value: b})
	}
	recs = append(recs, &kgo.Record{Key: []byte("r9"), Value: []byte{0xff, 0xff, 0xff}})
	if err := prod.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		t.Fatal(err)
	}
	prod.Close()

	for run := range 2 { // second run: same group, still replays (no commits)
		client, err := NewConsumer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		agg := NewAggregator()
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { Consume(runCtx, client, agg, slog.New(slog.DiscardHandler)); close(done) }()
		for agg.Snapshot().RoomsExpired == 0 || agg.Snapshot().DecodeErrors == 0 {
			if ctx.Err() != nil {
				t.Fatalf("run %d: timed out, snapshot %+v", run, agg.Snapshot())
			}
			time.Sleep(10 * time.Millisecond)
		}
		stop()
		<-done
		client.Close()
		if s := agg.Snapshot(); s.RoomsCreated != 2 || s.Sessions != 2 || s.DecodeErrors != 1 {
			b, _ := json.Marshal(s)
			t.Fatalf("run %d: %s", run, b)
		}
	}
}
