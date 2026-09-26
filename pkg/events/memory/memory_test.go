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
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: msg.Event, Consumer: "C",
		Handle: func(_ context.Context, m *events.Message) error {
			got.add(m)
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()
	return got.only(t)
}

// The key reaches the consumer.
func TestTheKeyIsCarriedToTheConsumer(t *testing.T) {
	got := deliver(t, &events.Message{
		Event: "orders.Placed", Key: "order-1", Payload: []byte(`{}`),
	})
	if got.Key != "order-1" {
		t.Errorf("delivered key = %q, want order-1", got.Key)
	}
}

// The deduplication ID reaches the consumer.
func TestTheDeduplicationIDIsCarriedToTheConsumer(t *testing.T) {
	got := deliver(t, &events.Message{
		Event: "orders.Placed", DedupID: "attempt-7", Payload: []byte(`{}`),
	})
	if got.DedupID != "attempt-7" {
		t.Errorf("delivered dedup id = %q, want attempt-7", got.DedupID)
	}
}

// Two publishes sharing a deduplication ID are two deliveries.
func TestTwoPublishesSharingADedupIDAreBothDelivered(t *testing.T) {
	tr := memory.New()
	got := &delivered{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C",
		Handle: func(_ context.Context, m *events.Message) error {
			got.add(m)
			return nil
		},
	}}); err != nil {
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

// Each subscriber gets its own copy of the message's metadata.
func TestEachSubscriberGetsItsOwnCopy(t *testing.T) {
	tr := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	seen := map[string]string{}
	for _, group := range []string{"a", "b"} {
		// A per-iteration copy: this module's go version shares loop variables.
		group := group
		if err := tr.Subscribe(ctx, []events.Subscription{{
			Event: "orders.Placed", Consumer: group, Group: events.Group(group),
			Handle: func(_ context.Context, m *events.Message) error {
				m.Metadata["touched-by"] = group
				mu.Lock()
				seen[group] = m.Metadata["touched-by"]
				mu.Unlock()
				return nil
			},
		}}); err != nil {
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

// The adapter names itself memory.Adapter and reads no options.
func TestTheAdapterNamesItselfAndReadsNoOptions(t *testing.T) {
	tr := memory.New()
	if tr.AdapterName() != memory.Adapter {
		t.Errorf("AdapterName = %q, want %q", tr.AdapterName(), memory.Adapter)
	}
	if got := tr.KnownOptions(); len(got) != 0 {
		t.Errorf("KnownOptions = %v, want none", got)
	}
}

// Drain is safe while another goroutine publishes; the loop makes a race likely.
func TestDrainIsSafeWhileAnotherGoroutinePublishes(t *testing.T) {
	const rounds = 2000

	tr := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C",
		Handle: func(context.Context, *events.Message) error { return nil },
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := tr.Publish(ctx, &events.Message{Event: "orders.Placed"}); err != nil {
				t.Errorf("publish: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			tr.Drain()
		}()
		wg.Wait()
	}
}

// Drain waits for a delivery that a handler started.
func TestDrainWaitsForADeliveryAHandlerStarted(t *testing.T) {
	tr := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var nested sync.WaitGroup
	parked := make(chan struct{}, 1)
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.consumer.dlq", Consumer: "Sink",
		Handle: func(context.Context, *events.Message) error {
			parked <- struct{}{}
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe sink: %v", err)
	}
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C",
		Handle: func(context.Context, *events.Message) error {
			nested.Add(1)
			defer nested.Done()
			return tr.Publish(ctx, &events.Message{Event: "orders.consumer.dlq"})
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := tr.Publish(ctx, &events.Message{Event: "orders.Placed"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	select {
	case <-parked:
	default:
		t.Fatal("Drain returned before the delivery the handler started")
	}
}
