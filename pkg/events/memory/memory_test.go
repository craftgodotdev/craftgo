package memory_test

import (
	"context"
	"sync"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

// delivered collects what a subscription was handed.
type delivered struct {
	mu   sync.Mutex
	msgs []*events.Message
}

func (d *delivered) add(msg *events.Message) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.msgs = append(d.msgs, msg)
}

func (d *delivered) only(t *testing.T) *events.Message {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.msgs) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(d.msgs))
	}
	return d.msgs[0]
}

// deliver publishes msg through a transport with one subscriber and
// returns what that subscriber was handed.
func deliver(t *testing.T, msg *events.Message) *events.Message {
	t.Helper()
	tr := memory.New()
	got := &delivered{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: msg.Event, Consumer: "C",
		Handle: func(_ context.Context, m *events.Message) error {
			got.add(m)
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()
	return got.only(t)
}

// The in-process transport does not ORDER on a key - its own package doc
// says two messages with the same key are not ordered, because each
// delivery runs on its own goroutine. But it CARRIES the key through to
// the consumer, which is the difference between a feature it has not got
// and a value it destroys: a consumer handed the key can act on it.
func TestTheKeyIsCarriedToTheConsumer(t *testing.T) {
	got := deliver(t, &events.Message{
		Event: "orders.Placed", Key: "order-1", Payload: []byte(`{}`),
	})
	if got.Key != "order-1" {
		t.Errorf("delivered key = %q, want order-1", got.Key)
	}
}

// Same for the deduplication ID: nothing here deduplicates, and the ID
// still reaches the consumer.
func TestTheDeduplicationIDIsCarriedToTheConsumer(t *testing.T) {
	got := deliver(t, &events.Message{
		Event: "orders.Placed", DedupID: "attempt-7", Payload: []byte(`{}`),
	})
	if got.DedupID != "attempt-7" {
		t.Errorf("delivered dedup id = %q, want attempt-7", got.DedupID)
	}
}

// Nothing deduplicates: two publishes sharing one ID are two deliveries.
// The row in the PublishOption table says so, and a transport that
// quietly started deduplicating would change delivery counts under every
// application that uses this for tests.
func TestTwoPublishesSharingADedupIDAreBothDelivered(t *testing.T) {
	tr := memory.New()
	got := &delivered{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C",
		Handle: func(_ context.Context, m *events.Message) error {
			got.add(m)
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := tr.Publish(context.Background(), &events.Message{
			Event: "orders.Placed", DedupID: "same", Payload: []byte(`{}`),
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	tr.Drain()

	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.msgs) != 2 {
		t.Errorf("delivered %d messages, want 2 - nothing here deduplicates", len(got.msgs))
	}
}

// A handler mutating what it was given must not be visible to the next
// subscriber, which is why each delivery gets its own copy.
func TestEachSubscriberGetsItsOwnCopy(t *testing.T) {
	tr := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	seen := map[string]string{}
	for _, group := range []string{"a", "b"} {
		// pkg/events declares go 1.21, so a loop variable is shared
		// across iterations: bind it per subscription or both closures
		// see the last value.
		group := group
		if err := tr.Subscribe(ctx, events.Subscription{
			Event: "orders.Placed", Consumer: group, Group: group,
			Handle: func(_ context.Context, m *events.Message) error {
				m.Metadata["touched-by"] = group
				mu.Lock()
				seen[group] = m.Metadata["touched-by"]
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("subscribe %s: %v", group, err)
		}
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Payload: []byte(`{}`),
		Metadata: map[string]string{"hops": "1"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if seen["a"] != "a" || seen["b"] != "b" {
		t.Errorf("subscribers saw %v - each delivery needs its own metadata map", seen)
	}
}

// The adapter names itself to the per-message option check and reads no
// options of its own, so an option addressed to `memory` is a mistake
// rather than something silently dropped.
func TestTheAdapterNamesItselfAndReadsNoOptions(t *testing.T) {
	tr := memory.New()
	if tr.AdapterName() != memory.Adapter {
		t.Errorf("AdapterName = %q, want %q", tr.AdapterName(), memory.Adapter)
	}
	if got := tr.KnownOptions(); len(got) != 0 {
		t.Errorf("KnownOptions = %v, want none", got)
	}
}
