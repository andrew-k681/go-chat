package stats

import (
	"context"
	"errors"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/andrew-k681/go-chat/server/gen/gochat/events/v1"
	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/events"
)

// NewConsumer joins the consumer group and reads the topic from the start.
//
// The aggregate lives in memory, so offsets are never committed: every start
// replays the whole topic (as far as retention allows) and rebuilds the same
// numbers. To keep state across restarts instead, store the aggregate and the
// offsets together in one transaction and resume from those offsets.
func NewConsumer(cfg config.Stats) (*kgo.Client, error) {
	return kgo.NewClient(append(events.ClientOpts(cfg.Kafka),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Kafka.Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.FetchMaxBytes(16<<20),
	)...)
}

// Consume polls until ctx is done or the client is closed.
func Consume(ctx context.Context, client *kgo.Client, agg *Aggregator, log *slog.Logger) error {
	for {
		fetches := client.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return ctx.Err()
		}
		fetches.EachError(func(topic string, partition int32, err error) {
			if !errors.Is(err, context.Canceled) {
				log.Warn("fetch", "topic", topic, "partition", partition, "err", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			var ev eventsv1.RoomEvent
			// A bad record is counted and skipped; it must not stop the consumer.
			if err := proto.Unmarshal(r.Value, &ev); err != nil {
				agg.DecodeError()
				log.Debug("undecodable record", "partition", r.Partition, "offset", r.Offset)
				return
			}
			agg.Apply(&ev)
		})
	}
}
