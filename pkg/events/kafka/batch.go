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
func (t *Transport) consumeBatches(ctx context.Context, cl *kgo.Client, sub events.Subscription) {
	defer t.releaseClient(cl)
	for {
		recs, ok := t.pollBatch(ctx, cl, sub)
		if !ok {
			return
		}
		if len(recs) == 0 {
			continue
		}
		t.deliverBatch(ctx, cl, sub, recs)
		if !t.share {
			if err := cl.CommitRecords(ctx, recs...); err != nil && ctx.Err() == nil {
				t.report(sub, nil, fmt.Errorf("kafka: commit: %w", err))
			}
		}
	}
}

// pollBatch returns the records of sub's next batch: in a classic group, until it holds
// Max or its first record has waited Wait; in a share group, one fetch's. ok is false
// once ctx ends or the client closes.
func (t *Transport) pollBatch(ctx context.Context, cl *kgo.Client, sub events.Subscription) (recs []*kgo.Record, ok bool) {
	size := sub.Batch.BatchSize
	pollCtx := ctx
	for len(recs) < size.Max {
		fetches := cl.PollRecords(pollCtx, size.Max-len(recs))
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil, false
		}
		fetches.EachError(func(topic string, _ int32, err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.report(sub, nil, fmt.Errorf("kafka: fetch %s: %w", topic, err))
			}
		})
		fetches.EachRecord(func(rec *kgo.Record) { recs = append(recs, rec) })
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
// contract is reported and skipped.
func (t *Transport) deliverBatch(ctx context.Context, cl *kgo.Client, sub events.Subscription, recs []*kgo.Record) {
	batch := make([]*events.Message, 0, len(recs))
	mine := make([]*kgo.Record, 0, len(recs))
	for _, rec := range recs {
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
	}
	if len(batch) == 0 {
		return
	}

	stop := t.holdOpen(ctx, cl, mine...)
	err := sub.Batch.Handle(ctx, batch)
	stop()

	if err != nil {
		t.report(sub, nil, err)
	}
	if !t.share {
		return
	}
	for i, rec := range mine {
		msg := batch[i]
		// The chain never sees the cap turn its Redeliver into a reject.
		if t.capped(msg) {
			t.report(sub, msg, fmt.Errorf("kafka: giving up on %s after %d deliveries - the chain asked for another and WithMaxDeliveries is %d", sub.Event, msg.Deliveries(), t.maxDeliveries))
		}
		rec.Ack(t.ackFor(msg))
	}
}
