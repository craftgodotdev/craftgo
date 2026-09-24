// Package memory is an in-process [events.Publisher] and [events.Subscriber] for tests,
// local development and single-binary deployments.
//
// Subscriptions sharing a group on one contract compete: each message reaches one member.
// Every delivery runs on its own goroutine with a fresh context, so nothing is ordered and
// the publisher's trace does not continue into the handler. Key and DedupID reach the
// consumer, and nothing acts on either.
package memory

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/craftgodotdev/craftgo/pkg/events"
)

var (
	_ events.Publisher      = (*Transport)(nil)
	_ events.Subscriber     = (*Transport)(nil)
	_ events.BatchPublisher = (*Transport)(nil)
	_ events.OptionAware    = (*Transport)(nil)
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
	members []events.Subscription
	next    atomic.Uint64
}

// Adapter is the name [events.WithAdapterOption] addresses this adapter by.
const Adapter = "memory"

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]: the adapter reads no options, so one
// addressed to it fails the publish.
func (t *Transport) KnownOptions() []string { return nil }

// Option configures a Transport.
type Option func(*Transport)

// WithErrorHandler installs a callback invoked when a handler returns an
// error.
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
	g.members = append(g.members, sub)
	idx := len(g.members) - 1
	t.mu.Unlock()

	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			t.mu.Lock()
			defer t.mu.Unlock()
			if g := t.groups[key]; g != nil && idx < len(g.members) {
				g.members[idx].Handle = nil
			}
		}()
	}
}

// Publish fans msg out to one member of every group subscribed to the
// contract. Delivery is asynchronous; use [Transport.Drain] to join.
func (t *Transport) Publish(_ context.Context, msg *events.Message) error {
	t.mu.RLock()
	var targets []events.Subscription
	for key, g := range t.groups {
		if key.event != msg.Event {
			continue
		}
		if sub, ok := g.pick(); ok {
			targets = append(targets, sub)
		}
	}
	t.mu.RUnlock()

	for _, sub := range targets {
		t.begin()
		go func(sub events.Subscription) {
			defer t.finish()
			// Each delivery gets its own copy, Metadata included.
			delivered := *msg
			delivered.Metadata = cloneMeta(msg.Metadata)
			if err := sub.Handle(context.Background(), &delivered); err != nil && t.onError != nil {
				t.onError(sub, &delivered, err)
			}
		}(sub)
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
func (g *group) pick() (events.Subscription, bool) {
	n := len(g.members)
	if n == 0 {
		return events.Subscription{}, false
	}
	start := int(g.next.Add(1)-1) % n
	for i := 0; i < n; i++ {
		sub := g.members[(start+i)%n]
		if sub.Handle != nil {
			return sub, true
		}
	}
	return events.Subscription{}, false
}

func cloneMeta(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
