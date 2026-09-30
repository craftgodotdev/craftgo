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

// The pull requests of a batch group. The one awaiting a batch's first message asks for one
// and lasts up to firstPull, heartbeating so a lost durable shows; each one filling the
// batch asks for the group's fetch size and lasts at most fillPull, so Drain and Stop act
// within it.
const (
	firstPull = 30 * time.Second
	fillPull  = time.Second
)

// pullLimits are a durable's caps on one pull request; zero is none.
type pullLimits struct {
	batch   int
	expires time.Duration
}

func pullLimitsOf(c jetstream.Consumer) pullLimits {
	info := c.CachedInfo()
	if info == nil {
		return pullLimits{}
	}
	return pullLimits{batch: info.Config.MaxRequestBatch, expires: info.Config.MaxRequestExpires}
}

// expiry bounds d by the durable's longest pull.
func (l pullLimits) expiry(d time.Duration) time.Duration {
	if l.expires > 0 {
		return min(d, l.expires)
	}
	return d
}

// consumeBatches starts fetching g's durable for sub, a batch subscription; each handler
// gets ctx. A failed fetch is reported and retried after a growing pause; the loop ends
// once the durable or its stream is gone.
func (j *JetStream) consumeBatches(ctx context.Context, g *groupPlan, sub events.Subscription, consumer jetstream.Consumer, ackWait time.Duration) *batchConsumer {
	fetchCtx, cancel := context.WithCancel(context.Background())
	b := &batchConsumer{cancel: cancel, closed: make(chan struct{})}
	whole := events.Subscription{Group: g.name}
	limits := pullLimitsOf(consumer)
	go func() {
		defer close(b.closed)
		defer cancel()
		pause := time.Duration(0)
		for b.current() == batchRunning {
			hold := holdBatch(ackWait)
			ms, err := j.gather(fetchCtx, g, sub, consumer, limits, ackWait, hold)
			if b.current() == batchStopped {
				hold.release()
				for _, m := range ms {
					_ = m.Nak()
				}
				return
			}
			if len(ms) > 0 {
				j.deliverBatch(ctx, sub, ms, hold)
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

// consumerGone reports whether err, a failed fetch, comes of the durable or its stream no
// longer existing, asking the server when err does not say.
func (j *JetStream) consumerGone(ctx context.Context, consumer jetstream.Consumer, err error) bool {
	gone := func(err error) bool {
		return errors.Is(err, jetstream.ErrConsumerDeleted) ||
			errors.Is(err, jetstream.ErrConsumerNotFound) ||
			errors.Is(err, jetstream.ErrStreamNotFound)
	}
	if gone(err) {
		return true
	}
	probeCtx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()
	_, err = consumer.Info(probeCtx)
	return gone(err)
}

// gather fetches sub's next batch into hold: one pull waits for its first message, then
// pulls fill it until it holds Max or Wait has passed since that message. A message of a
// subject sub does not consume goes back at once, for a replica that does. The error is
// nil for pulls that ended by running out of time or by ctx.
func (j *JetStream) gather(ctx context.Context, g *groupPlan, sub events.Subscription, consumer jetstream.Consumer, limits pullLimits, ackWait time.Duration, hold *batchHold) ([]jetstream.Msg, error) {
	subject := j.subject(sub.Event)
	var ms []jetstream.Msg
	take := func(res jetstream.MessageBatch) error {
		for m := range res.Messages() {
			if m.Subject() != subject {
				j.report(events.Subscription{Group: g.name}, nil, fmt.Errorf("nats: durable %q delivered subject %s, which no consumer in this process handles - handed back for a replica that does", g.name, m.Subject()))
				_ = m.NakWithDelay(ackWait)
				continue
			}
			hold.add(m)
			ms = append(ms, m)
		}
		if err := res.Error(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return nil
	}

	firstCtx, cancel := context.WithTimeout(ctx, limits.expiry(firstPull))
	defer cancel()
	res, err := consumer.Fetch(1, jetstream.FetchContext(firstCtx))
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil
		}
		return nil, err
	}
	if err := take(res); err != nil || len(ms) == 0 {
		return ms, err
	}

	size := sub.Batch.BatchSize
	deadline := time.Now().Add(size.Wait)
	for len(ms) < size.Max && ctx.Err() == nil {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		n := min(size.Max-len(ms), g.config.fetchSize)
		if limits.batch > 0 {
			n = min(n, limits.batch)
		}
		res, err := consumer.Fetch(n, jetstream.FetchMaxWait(limits.expiry(min(left, fillPull))))
		if err != nil {
			return ms, err
		}
		if err := take(res); err != nil {
			return ms, err
		}
	}
	return ms, nil
}

// deliverBatch hands ms to sub's batch handler, then answers each message as the chain
// decided, as [JetStream.deliver] does for one.
func (j *JetStream) deliverBatch(ctx context.Context, sub events.Subscription, ms []jetstream.Msg, hold *batchHold) {
	batch := make([]*events.Message, len(ms))
	for i, m := range ms {
		batch[i] = decodeFrom(sub.Event, m.Headers(), m.Data())
		batch[i].SetDeliveries(deliveryCount(m))
	}
	err := sub.Batch.Handle(ctx, batch)
	hold.release()

	if err != nil {
		j.report(sub, nil, err)
	}
	for i, m := range ms {
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
