package kafka

import (
	"context"

	"github.com/twmb/franz-go/pkg/kgo"
)

// recordKey is this adapter's context key for the record behind a
// delivery. It is UNEXPORTED and its type is distinct from every other
// adapter's, so a Kafka-typed read on another transport's delivery cannot
// find anything - it is not that the lookup misses, it is that no value
// of this type was ever stored. A shared or exported key would let a
// Kafka-only middleware installed on NATS read a nil and carry on as if
// the record simply had nothing in it.
type recordKey struct{}

// RecordFrom returns the Kafka record behind the delivery ctx belongs to,
// for a middleware that needs what [events.Message] does not carry: the
// partition, the offset, the record timestamp, a header this adapter did
// not map.
//
//	func PartitionLog(next craftevents.Handler) craftevents.Handler {
//	    return func(ctx context.Context, msg *craftevents.Message) error {
//	        if rec, ok := kafka.RecordFrom(ctx); ok {
//	            log.Info("delivery", "partition", rec.Partition, "offset", rec.Offset)
//	        }
//	        return next(ctx, msg)
//	    }
//	}
//
// It reports false on a delivery from any other transport, and on a
// context that is not a delivery at all.
//
// # Do not keep the record past the handler's return
//
// Read what you need and let it go. The bytes are safe to hold - franz-go
// clones a decompressed record before recycling its buffer - but in share
// mode the record's ACK STATE is not: the next poll finalises the
// previous one, auto-accepting every record that was not explicitly
// answered for. A record held past that point reports a
// [kgo.Record.DeliveryCount] of 0 and its Ack does nothing, both silently.
//
// Decide through [events.Message] - Settle, Redeliver, Reject - rather
// than through the record. That is what the disposition is for, and it is
// answered for at the right moment whatever the transport.
func RecordFrom(ctx context.Context) (*kgo.Record, bool) {
	rec, ok := ctx.Value(recordKey{}).(*kgo.Record)
	return rec, ok
}

// MustRecord is [RecordFrom] for a middleware that only ever runs on this
// transport. It panics when there is no record, which the bus turns into
// a [events.PanicError] naming the consumer, the group and the contract -
// so a middleware installed on the wrong transport says so on its FIRST
// message rather than quietly doing nothing for a week.
//
// Use it when running on another transport is a wiring mistake. Use
// [RecordFrom] when the middleware is meant to work on several and to
// skip what it cannot read.
func MustRecord(ctx context.Context) *kgo.Record {
	rec, ok := RecordFrom(ctx)
	if !ok {
		panic("kafka: no Kafka record on this context - this middleware is installed on a transport that is not kafka")
	}
	return rec
}

// withRecord returns ctx carrying rec, for the delivery of that record.
func withRecord(ctx context.Context, rec *kgo.Record) context.Context {
	return context.WithValue(ctx, recordKey{}, rec)
}
