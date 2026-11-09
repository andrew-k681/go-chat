package events

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/hub"
)

// Producer publishes hub events to Kafka. Stats are best-effort: when Kafka
// is slow or down, events are dropped and calls keep working.
type Producer struct {
	client  *kgo.Client
	enc     *Encoder
	log     *slog.Logger
	dropped atomic.Int64
}

func NewProducer(k config.Kafka, key config.Secret, log *slog.Logger) (*Producer, error) {
	opts := append(ClientOpts(k),
		kgo.DefaultProduceTopic(k.Topic),
		// Bounded memory. TryProduce fails fast instead of blocking when full.
		kgo.MaxBufferedRecords(10_000),
		kgo.ProducerLinger(50*time.Millisecond),
		kgo.RecordDeliveryTimeout(30*time.Second),
	)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Producer{client: client, enc: NewEncoder(key), log: log}, nil
}

// Emit never blocks, so it is safe as hub.Options.OnEvent. The client is
// idempotent by default: retries don't create duplicates.
func (p *Producer) Emit(ev hub.Event) {
	msg := p.enc.Encode(ev)
	if msg == nil {
		return
	}
	value, err := proto.Marshal(msg)
	if err != nil {
		p.drop(err)
		return
	}
	rec := &kgo.Record{
		Key:     []byte(msg.RoomRef), // same room → same partition → ordered
		Value:   value,
		Headers: []kgo.RecordHeader{{Key: "content-type", Value: []byte(ContentType)}},
	}
	p.client.TryProduce(context.Background(), rec, func(_ *kgo.Record, err error) {
		if err != nil {
			p.drop(err)
		}
	})
}

// drop logs the first failure and then every 1000th, so an outage can't flood logs.
func (p *Producer) drop(err error) {
	if n := p.dropped.Add(1); n == 1 || n%1000 == 0 {
		p.log.Warn("room event dropped", "err", err, "dropped_total", n)
	}
}

// Dropped reports how many events failed to reach Kafka.
func (p *Producer) Dropped() int64 { return p.dropped.Load() }

// Close flushes buffered events until ctx is done.
func (p *Producer) Close(ctx context.Context) {
	if err := p.client.Flush(ctx); err != nil {
		p.log.Warn("flushing room events", "err", err)
	}
	p.client.Close()
}
