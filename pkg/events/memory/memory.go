// Package memory is an in-process [events.Publisher] / [events.Subscriber]
// for tests, local development, and single-binary deployments.
//
// Subscriptions sharing a [events.Subscription.Group] for the same
// contract form one competing-consumer group: each message reaches
// exactly one member.
//
// Delivery runs on its own goroutine with a fresh context, so a trace
// opened by the publisher does not continue into the handler, and two
// messages with the same key are not ordered.
package memory

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/craftgodotdev/craftgo/pkg/events"
)

// A Transport is a full transport: it publishes, subscribes, takes a
// batch in one call, and names itself to the per-message option check.
// Asserted here so a change to the runtime interfaces fails this package
// rather than a user's wiring.
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

	// wg tracks in-flight deliveries for [Transport.Drain].
	wg sync.WaitGroup

	onError func(sub events.Subscription, msg *events.Message, err error)
}

type groupKey struct{ event, group string }

// group is one competing-consumer set.
type group struct {
	members []events.Subscription
	next    atomic.Uint64
}

// Adapter is the name [events.WithAdapterOption] addresses this adapter
// by.
const Adapter = "memory"

// AdapterName implements [events.OptionAware].
func (t *Transport) AdapterName() string { return Adapter }

// KnownOptions implements [events.OptionAware]. There is no broker here
// to have a feature of its own, so an option addressed to `memory` is
// always a mistake and fails the publish. An option addressed to a real
// adapter is ignored, which is what lets a test run the same publishing
// code in process.
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
	for _, o := range opts {
		o(t)
	}
	return t
}

// Subscribe registers sub until ctx is cancelled.
func (t *Transport) Subscribe(ctx context.Context, sub events.Subscription) error {
	key := groupKey{sub.Event, sub.GroupName()}
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
	return nil
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
		t.wg.Add(1)
		go func(sub events.Subscription) {
			defer t.wg.Done()
			// Own copy per delivery: a handler mutating Metadata must
			// not be visible to the next subscriber.
			delivered := *msg
			delivered.Metadata = cloneMeta(msg.Metadata)
			if err := sub.Handle(context.Background(), &delivered); err != nil && t.onError != nil {
				t.onError(sub, &delivered, err)
			}
		}(sub)
	}
	return nil
}

// Drain blocks until every delivery started so far has finished.
func (t *Transport) Drain() { t.wg.Wait() }

// pick returns the next live member of the group, round-robin. A member
// whose context was cancelled carries a nil Handle and is skipped.
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

// PublishBatch takes the whole batch in one call, so the transport craftgo
// ships exercises the same path a broker adapter does rather than leaving
// every project on the one-at-a-time fallback.
func (t *Transport) PublishBatch(ctx context.Context, msgs []*events.Message) error {
	for i, msg := range msgs {
		err := t.Publish(ctx, msg)
		if err == nil {
			continue
		}
		// A bare error here would read as "nothing arrived" while the
		// messages before this one have already been delivered. Name the
		// tail instead, which is what [events.BatchPublisher] asks for.
		unsent := make([]int, 0, len(msgs)-i)
		for j := i; j < len(msgs); j++ {
			unsent = append(unsent, j)
		}
		return &events.PartialPublishError{Sent: i, Unsent: unsent, Event: msg.Event, Err: err}
	}
	return nil
}
