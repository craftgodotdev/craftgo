package nats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

// SubscribesBatches implements [events.BatchSubscriber]: a batch subscription's group
// fetches its durable one batch at a time.
func (j *JetStream) SubscribesBatches() bool { return true }

// batchState is how far a [batchConsumer] is from stopping.
type batchState int

const (
	batchRunning batchState = iota
	// batchDraining hands the messages already fetched over, then stops.
	batchDraining
	// batchStopped hands nothing more over; the messages fetched go back at once.
	batchStopped
)

// batchConsumer fetches a batch group's durable one batch at a time and hands each to
// the group's batch subscription. It stands in for the group's
// [jetstream.ConsumeContext]: Drain ends it after the batch in hand, Stop at once.
type batchConsumer struct {
	cancel context.CancelFunc // ends the fetch in flight
	closed chan struct{}

	mu    sync.Mutex
	state batchState
}

func (b *batchConsumer) Stop()                   { b.end(batchStopped) }
func (b *batchConsumer) Drain()                  { b.end(batchDraining) }
func (b *batchConsumer) Closed() <-chan struct{} { return b.closed }

// end moves b to state, never back, and ends the fetch in flight.
func (b *batchConsumer) end(state batchState) {
	b.mu.Lock()
	b.state = max(b.state, state)
	b.mu.Unlock()
	b.cancel()
}

func (b *batchConsumer) current() batchState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// batchRetry bounds the pause between failed fetches.
const (
	batchRetryMin = 100 * time.Millisecond
	batchRetryMax = 5 * time.Second
)

// consumeBatches starts fetching g's durable for sub, a batch subscription; each handler
// gets ctx. A fetch that fails is reported and retried after a growing pause; the loop
// ends when the durable or its stream is gone.
func (j *JetStream) consumeBatches(ctx context.Context, g *groupPlan, sub events.Subscription, consumer jetstream.Consumer, ackWait time.Duration) *batchConsumer {
	fetchCtx, cancel := context.WithCancel(context.Background())
	b := &batchConsumer{cancel: cancel, closed: make(chan struct{})}
	whole := events.Subscription{Group: g.name}
	go func() {
		defer close(b.closed)
		defer cancel()
		pause := time.Duration(0)
		for b.current() == batchRunning {
			hold := holdBatch(ackWait)
			ms, err := fetchBatch(fetchCtx, consumer, sub.Batch.BatchSize, hold)
			if b.current() == batchStopped {
				hold.release()
				for _, m := range ms {
					_ = m.Nak()
				}
				return
			}
			if len(ms) > 0 {
				j.deliverBatch(ctx, g, sub, ackWait, ms, hold)
			} else {
				hold.release()
			}
			if err == nil {
				pause = 0
				continue
			}
			if j.consumerGone(ctx, consumer, err) {
				return
			}
			j.report(whole, nil, fmt.Errorf("nats: fetch group %q: %w", g.name, err))
			pause = min(max(2*pause, batchRetryMin), batchRetryMax)
			select {
			case <-fetchCtx.Done():
			case <-time.After(pause):
			}
		}
	}()
	return b
}

// consumerGone reports whether err, a failed fetch, means the durable or its stream no
// longer exists. A missed heartbeat asks the server.
func (j *JetStream) consumerGone(ctx context.Context, consumer jetstream.Consumer, err error) bool {
	gone := func(err error) bool {
		return errors.Is(err, jetstream.ErrConsumerDeleted) ||
			errors.Is(err, jetstream.ErrConsumerNotFound) ||
			errors.Is(err, jetstream.ErrStreamNotFound)
	}
	if gone(err) {
		return true
	}
	if !errors.Is(err, jetstream.ErrNoHeartbeat) {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()
	_, err = consumer.Info(probeCtx)
	return gone(err)
}

// fetchBatch fetches up to size.Max messages, for at most size.Wait, adding each to hold
// as it arrives. Its error is nil for a fetch that ended by running out of time or by ctx.
func fetchBatch(ctx context.Context, consumer jetstream.Consumer, size events.BatchSize, hold *batchHold) ([]jetstream.Msg, error) {
	waitCtx, cancel := context.WithTimeout(ctx, size.Wait)
	defer cancel()
	res, err := consumer.Fetch(size.Max, jetstream.FetchContext(waitCtx))
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}
	var ms []jetstream.Msg
	for m := range res.Messages() {
		hold.add(m)
		ms = append(ms, m)
	}
	if err := res.Error(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return ms, err
	}
	return ms, nil
}

// deliverBatch hands ms to sub's batch handler, then answers each message as the chain
// decided, as [JetStream.deliver] does for one; a message of a subject sub does not
// consume goes back for a replica that does.
func (j *JetStream) deliverBatch(ctx context.Context, g *groupPlan, sub events.Subscription, ackWait time.Duration, ms []jetstream.Msg, hold *batchHold) {
	subject := j.subject(sub.Event)
	batch := make([]*events.Message, 0, len(ms))
	mine := make([]jetstream.Msg, 0, len(ms))
	for _, m := range ms {
		if m.Subject() != subject {
			j.report(events.Subscription{Group: g.name}, nil, fmt.Errorf("nats: durable %q delivered subject %s, which no consumer in this process handles - handed back for a replica that does", g.name, m.Subject()))
			_ = m.NakWithDelay(ackWait)
			continue
		}
		msg := decodeFrom(sub.Event, m.Headers(), m.Data())
		msg.SetDeliveries(deliveryCount(m))
		batch = append(batch, msg)
		mine = append(mine, m)
	}
	if len(batch) == 0 {
		hold.release()
		return
	}
	err := sub.Batch.Handle(ctx, batch)
	hold.release()

	if err != nil {
		j.report(sub, nil, err)
	}
	for i, m := range mine {
		msg := batch[i]
		if j.capped(msg) {
			j.report(sub, msg, fmt.Errorf("nats: giving up on %s after %d deliveries - the chain asked for another and WithMaxDeliveries is %d", sub.Event, msg.Deliveries(), j.maxDeliveries))
		}
		if ackErr := j.answer(m, msg); ackErr != nil {
			j.report(sub, msg, fmt.Errorf("nats: answering for %s: %w", sub.Event, ackErr))
		}
	}
}

// batchHold resets the redelivery timer of each message it holds, every half AckWait,
// from the message's arrival until release returns.
type batchHold struct {
	mu      sync.Mutex
	msgs    []jetstream.Msg
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

// holdBatch starts a hold for a batch of a durable with ackWait.
func holdBatch(ackWait time.Duration) *batchHold {
	h := &batchHold{done: make(chan struct{}), stopped: make(chan struct{})}
	every := ackWait / 2
	if every <= 0 {
		close(h.stopped)
		return h
	}
	go func() {
		defer close(h.stopped)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-h.done:
				return
			case <-t.C:
				h.mu.Lock()
				held := h.msgs
				h.mu.Unlock()
				for _, m := range held {
					_ = m.InProgress()
				}
			}
		}
	}()
	return h
}

func (h *batchHold) add(m jetstream.Msg) {
	h.mu.Lock()
	h.msgs = append(h.msgs, m)
	h.mu.Unlock()
}

// release ends the hold and returns once no reset can follow.
func (h *batchHold) release() {
	h.once.Do(func() { close(h.done) })
	<-h.stopped
}
