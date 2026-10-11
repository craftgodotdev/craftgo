package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// SubscribesBatches implements [events.BatchSubscriber]. A classic group gathers a batch
// over several fetches; a share group hands over one fetch's records, at most the
// batch's Max, since the client accepts on the next fetch what the last left unanswered.
func (t *Transport) SubscribesBatches() bool { return true }

// consumeBatches is a batch subscription's read loop. A classic group commits a batch's
// records, failed or not, once the handler has returned; a share group answers each.
// Once ctx ends or the transport closes, nothing more is handed over or polled, and the
// records the shutdown kept from the handler or cut short go back to the broker.
func (t *Transport) consumeBatches(ctx context.Context, cl *kgo.Client, sub events.Subscription) {
	defer t.releaseClient(cl)
	for !t.stopping(ctx) {
		recs, ok := t.pollBatch(ctx, cl, sub)
		if !ok {
			t.handBack(recs...)
			return
		}
		if len(recs) == 0 {
			continue
		}
		back := t.deliverBatch(ctx, cl, sub, recs)
		t.commit(ctx, cl, sub, committable(recs, back))
	}
}

// pollBatch returns the records of sub's next batch: in a classic group, until it holds
// Max or its first record has waited Wait; in a share group, one fetch's. ok is false
// once ctx ends or the transport closes, with the records polled so far.
func (t *Transport) pollBatch(ctx context.Context, cl *kgo.Client, sub events.Subscription) (recs []*kgo.Record, ok bool) {
	size := sub.Batch.BatchSize
	pollCtx := ctx
	for len(recs) < size.Max {
		fetches := cl.PollRecords(pollCtx, size.Max-len(recs))
		recs = append(recs, fetches.Records()...)
		if fetches.IsClientClosed() || t.stopping(ctx) {
			return recs, false
		}
		fetches.EachError(func(topic string, _ int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.report(sub, nil, fmt.Errorf("kafka: fetch %s: %w", topic, err))
			}
		})
		if t.share || pollCtx.Err() != nil {
			break
		}
		if len(recs) > 0 && pollCtx == ctx {
			var cancel context.CancelFunc
			pollCtx, cancel = context.WithTimeout(ctx, size.Wait)
			defer cancel()
		}
	}
	return recs, true
}

// deliverBatch hands recs to sub's batch handler and, in a share group, answers each
// record as the chain decided, as [Transport.deliver] does for one. A record of another
// contract is reported and skipped. back names the records returned to the broker
// unanswered: every one when the shutdown came first, and each the shutdown cut short.
func (t *Transport) deliverBatch(ctx context.Context, cl *kgo.Client, sub events.Subscription, recs []*kgo.Record) (back []bool) {
	back = make([]bool, len(recs))
	batch := make([]*events.Message, 0, len(recs))
	mine := make([]*kgo.Record, 0, len(recs))
	at := make([]int, 0, len(recs))
	for i, rec := range recs {
		msg := decode(sub.Event, rec)
		msg.SetDeliveries(int(rec.DeliveryCount()))
		if msg.Event != sub.Event {
			t.report(sub, msg, fmt.Errorf("kafka: topic %s carried %s, which %s does not consume - skipped", rec.Topic, msg.Event, sub.Consumer))
			if t.share {
				rec.Ack(kgo.AckAccept)
			}
			continue
		}
		batch = append(batch, msg)
		mine = append(mine, rec)
		at = append(at, i)
	}
	if len(batch) == 0 {
		return back
	}
	if t.stopping(ctx) {
		for _, i := range at {
			back[i] = true
		}
		t.handBack(mine...)
		return back
	}

	stop := t.holdOpen(ctx, cl, mine...)
	err := sub.Batch.Handle(ctx, batch)
	stop()

	if err != nil {
		t.report(sub, nil, err)
	}
	for k, rec := range mine {
		msg := batch[k]
		if interrupted(ctx, msg, itemErr(err, k)) {
			back[at[k]] = true
			t.handBack(rec)
			continue
		}
		if !t.share {
			continue
		}
		// The chain never sees the cap turn its Redeliver into a reject.
		if t.capped(msg) {
			t.report(sub, msg, fmt.Errorf("kafka: giving up on %s after %d deliveries - the chain asked for another and WithMaxDeliveries is %d", sub.Event, msg.Deliveries(), t.maxDeliveries))
		}
		rec.Ack(t.ackFor(msg))
	}
	return back
}

// itemErr is the error err, a batch handler's, holds for message i of the batch: the
// entry an [events.ItemErrors] has for it, or err itself when it is no ItemErrors.
func itemErr(err error, i int) error {
	var failed events.ItemErrors
	if errors.As(err, &failed) {
		return failed[i]
	}
	return err
}

// committable is recs without, per partition, the first record back names and every one
// after it: a classic group's offset is a high-water mark, so committing a later record
// would commit that one too. recs holds each partition's records in offset order.
func committable(recs []*kgo.Record, back []bool) []*kgo.Record {
	type partition struct {
		topic string
		id    int32
	}
	var stopped map[partition]bool
	out := make([]*kgo.Record, 0, len(recs))
	for i, rec := range recs {
		p := partition{rec.Topic, rec.Partition}
		if back[i] {
			if stopped == nil {
				stopped = map[partition]bool{}
			}
			stopped[p] = true
		}
		if stopped[p] {
			continue
		}
		out = append(out, rec)
	}
	return out
}
