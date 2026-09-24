package kafka

import (
	"context"

	"github.com/twmb/franz-go/pkg/kgo"
)

// recordKey is the context key for a Kafka delivery's record.
type recordKey struct{}

// RecordFrom returns the record behind a Kafka delivery, if ctx is one.
func RecordFrom(ctx context.Context) (*kgo.Record, bool) {
	rec, ok := ctx.Value(recordKey{}).(*kgo.Record)
	return rec, ok
}

// MustRecord is like [RecordFrom] but panics if ctx carries no record.
func MustRecord(ctx context.Context) *kgo.Record {
	rec, ok := RecordFrom(ctx)
	if !ok {
		panic("kafka: no Kafka record on this context - this middleware is installed on a transport that is not kafka")
	}
	return rec
}

func withRecord(ctx context.Context, rec *kgo.Record) context.Context {
	return context.WithValue(ctx, recordKey{}, rec)
}
