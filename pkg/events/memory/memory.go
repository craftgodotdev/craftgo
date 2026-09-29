// Package memory is an in-process [events.Publisher] and [events.Subscriber] for tests,
// local development and single-binary deployments.
//
// Subscriptions sharing a group on one contract compete: each message reaches one member.
// Every delivery runs on its own goroutine with a fresh context, so nothing is ordered and
// the publisher's trace does not continue into the handler; a batch subscription gets a
// goroutine per batch. Key and DedupID reach the consumer, and nothing acts on either.
package memory

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/events"
)

var (
	_ events.Publisher       = (*Transport)(nil)
	_ events.Subscriber      = (*Transport)(nil)
	_ events.BatchPublisher  = (*Transport)(nil)
	_ events.BatchSubscriber = (*Transport)(nil)
	_ events.OptionAware     = (*Transport)(nil)
)

// Transport is an in-process publish/subscribe transport.
type Transport struct {
	mu     sync.RWMutex
	groups map[groupKey]*group

	// inflight counts started, unfinished deliveries and idle broadcasts at zero. Publish
	// may count one while another goroutine waits in Drain.
	inflightMu sync.Mutex
	idle       *sync.Cond
	inflight   int

	onError func(sub events.Subscription, msg *events.Message, err error)
}

type groupKey struct {
	event string
	group events.Group
}

// group is one competing-consumer set.
type group struct {
	members []*member
	next    atomic.Uint64
}

// member is one subscription of a group; batch gathers a batch subscription's messages.
type member struct {
	sub   events.Subscription
	batch *batcher
	done  bool
}

// Adapter is the name [events.WithAdapterOption] addresses this adapter by.
const Adapter = "memory"

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: the adapter reads no options, so one
// addressed to it fails the publish.
func (t *Transport) KnownOptions() []string { return nil }

// SubscribesBatches implements [events.BatchSubscriber].
func (t *Transport) SubscribesBatches() bool { return true }

// Option configures a Transport.
type Option func(*Transport)

// WithErrorHandler installs a callback invoked when a handler returns an
// error; a batch handler's error arrives once, with msg nil.
func WithErrorHandler(fn func(sub events.Subscription, msg *events.Message, err error)) Option {
	return func(t *Transport) { t.onError = fn }
}

// New returns an empty in-process transport.
func New(opts ...Option) *Transport {
	t := &Transport{groups: map[groupKey]*group{}}
	t.idle = sync.NewCond(&t.inflightMu)
	for _, o := range opts {
		o(t)
	}
	return t
}

// Subscribe registers every subscription in the batch until ctx is cancelled.
func (t *Transport) Subscribe(ctx context.Context, subs []events.Subscription) error {
	for _, sub := range subs {
		t.register(ctx, sub)
	}
	return nil
}

// register adds sub to its competing-consumer group and stops delivering to it once ctx
// is cancelled.
func (t *Transport) register(ctx context.Context, sub events.Subscription) {
	key := groupKey{sub.Event, sub.Group}
	t.mu.Lock()
	g := t.groups[key]
	if g == nil {
		g = &group{}
		t.groups[key] = g
	}
	m := &member{sub: sub}
	if sub.Batch != nil {
		m.batch = &batcher{t: t, sub: sub}
	}
	g.members = append(g.members, m)
	t.mu.Unlock()

	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			t.mu.Lock()
			defer t.mu.Unlock()
			m.done = true
		}()
	}
}

// Publish fans msg out to one member of every group subscribed to the
// contract. Delivery is asynchronous; use [Transport.Drain] to join.
func (t *Transport) Publish(_ context.Context, msg *events.Message) error {
	t.mu.RLock()
	var targets []*member
	for key, g := range t.groups {
		if key.event != msg.Event {
			continue
		}
		if m, ok := g.pick(); ok {
			targets = append(targets, m)
		}
	}
	t.mu.RUnlock()

	for _, m := range targets {
		t.begin()
		// Each delivery gets its own copy, Metadata included.
		delivered := *msg
		delivered.Metadata = maps.Clone(msg.Metadata)
		if m.batch != nil {
			m.batch.add(&delivered)
			continue
		}
		go func(sub events.Subscription) {
			defer t.finish()
			if err := sub.Handle(context.Background(), &delivered); err != nil && t.onError != nil {
				t.onError(sub, &delivered, err)
			}
		}(m.sub)
	}
	return nil
}

// Drain blocks until every delivery started so far has finished. It is
// safe to call while another goroutine publishes.
func (t *Transport) Drain() {
	t.inflightMu.Lock()
	defer t.inflightMu.Unlock()
	for t.inflight > 0 {
		t.idle.Wait()
	}
}

// begin counts a delivery about to start.
func (t *Transport) begin() {
	t.inflightMu.Lock()
	t.inflight++
	t.inflightMu.Unlock()
}

// finish counts a delivery that has ended, waking Drain when the last one does.
func (t *Transport) finish() {
	t.inflightMu.Lock()
	t.inflight--
	if t.inflight == 0 {
		t.idle.Broadcast()
	}
	t.inflightMu.Unlock()
}

// pick returns the next live member of the group, round-robin, skipping one whose
// context was cancelled.
func (g *group) pick() (*member, bool) {
	n := len(g.members)
	if n == 0 {
		return nil, false
	}
	start := int(g.next.Add(1)-1) % n
	for i := 0; i < n; i++ {
		m := g.members[(start+i)%n]
		if !m.done {
			return m, true
		}
	}
	return nil, false
}

// batcher gathers a batch subscription's messages: a batch goes to the handler once it
// holds Max messages, or Wait after its first message arrived.
type batcher struct {
	t   *Transport
	sub events.Subscription

	mu      sync.Mutex
	pending []*events.Message
	timer   *time.Timer
}

// add queues msg. A full batch goes at once; the first message of a batch starts its Wait.
func (b *batcher) add(msg *events.Message) {
	b.mu.Lock()
	b.pending = append(b.pending, msg)
	var full []*events.Message
	switch {
	case len(b.pending) >= b.sub.Batch.Max:
		full = b.take()
	case len(b.pending) == 1:
		b.timer = time.AfterFunc(b.sub.Batch.Wait, b.expire)
	}
	b.mu.Unlock()
	if full != nil {
		go b.handle(full)
	}
}

// expire sends on the batch whose Wait has passed.
func (b *batcher) expire() {
	b.mu.Lock()
	batch := b.take()
	b.mu.Unlock()
	if len(batch) > 0 {
		b.handle(batch)
	}
}

// take empties the pending batch and stops its timer; b.mu is held.
func (b *batcher) take() []*events.Message {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	batch := b.pending
	b.pending = nil
	return batch
}

// handle runs the batch handler, then ends the batch's deliveries.
func (b *batcher) handle(batch []*events.Message) {
	defer func() {
		for range batch {
			b.t.finish()
		}
	}()
	if err := b.sub.Batch.Handle(context.Background(), batch); err != nil && b.t.onError != nil {
		b.t.onError(b.sub, nil, err)
	}
}

// PublishBatch implements [events.BatchPublisher] by publishing msgs in order.
func (t *Transport) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	for i, msg := range msgs {
		err := t.Publish(ctx, msg)
		if err == nil {
			continue
		}
		return events.UnsentFrom(i, msgs, err)
	}
	return nil
}
