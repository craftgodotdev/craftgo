package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

// batches collects the batches a subscription was handed.
type batches struct {
	mu  sync.Mutex
	got [][]*events.Message
}

func (b *batches) handle(_ context.Context, batch []*events.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, batch)
	return nil
}

func (b *batches) sizes() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]int, len(b.got))
	for i, batch := range b.got {
		out[i] = len(batch)
	}
	return out
}

// subscribeBatch subscribes handle to a.B in batches of size, until the test ends.
func subscribeBatch(t *testing.T, tr *memory.Transport, size events.BatchSize, handle events.BatchHandler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "a.B", Consumer: "C", Group: "g",
		Batch: &events.Batch{BatchSize: size, Handle: handle},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

// publishN publishes n messages of a.B.
func publishN(t *testing.T, tr *memory.Transport, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := tr.Publish(context.Background(), &events.Message{Event: "a.B"}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
}

// A batch goes once it holds Max messages, without waiting out its Wait.
func TestABatchGoesOnceFull(t *testing.T) {
	tr := memory.New()
	got := &batches{}
	subscribeBatch(t, tr, events.BatchSize{Max: 3, Wait: time.Hour}, got.handle)
	publishN(t, tr, 3)
	tr.Drain()
	if sizes := got.sizes(); len(sizes) != 1 || sizes[0] != 3 {
		t.Errorf("batch sizes = %v, want one batch of 3", sizes)
	}
}

// A short batch goes once its first message has waited Wait.
func TestAShortBatchGoesAfterItsWait(t *testing.T) {
	tr := memory.New()
	got := &batches{}
	subscribeBatch(t, tr, events.BatchSize{Max: 10, Wait: 100 * time.Millisecond}, got.handle)
	publishN(t, tr, 2)
	tr.Drain()
	if sizes := got.sizes(); len(sizes) != 1 || sizes[0] != 2 {
		t.Errorf("batch sizes = %v, want one batch of 2", sizes)
	}
}

// A batch's error reaches the error handler once, with no message.
func TestABatchErrorIsReportedOnce(t *testing.T) {
	var mu sync.Mutex
	var reported []*events.Message
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, msg *events.Message, _ error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, msg)
	}))
	subscribeBatch(t, tr, events.BatchSize{Max: 2, Wait: time.Hour}, func(context.Context, []*events.Message) error {
		return errors.New("store down")
	})
	publishN(t, tr, 2)
	tr.Drain()
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || reported[0] != nil {
		t.Errorf("reported %v, want one report without a message", reported)
	}
}

// A batch subscription whose context ended gets nothing more.
func TestABatchSubscriptionStopsWithItsContext(t *testing.T) {
	tr := memory.New()
	got := &batches{}
	ctx, cancel := context.WithCancel(context.Background())
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "a.B", Consumer: "C", Group: "g",
		Batch: &events.Batch{BatchSize: events.BatchSize{Max: 1, Wait: time.Hour}, Handle: got.handle},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		publishN(t, tr, 1)
		tr.Drain()
		if len(got.sizes()) == 0 {
			break
		}
		got = &batches{}
		time.Sleep(time.Millisecond)
	}
	if sizes := got.sizes(); len(sizes) != 0 {
		t.Errorf("batches after cancel = %v, want none", sizes)
	}
}

// Cancelling a batch subscription hands its filling batch over at once.
func TestCancellingABatchSubscriptionHandsItsBatchOver(t *testing.T) {
	tr := memory.New()
	got := &batches{}
	ctx, cancel := context.WithCancel(context.Background())
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "a.B", Consumer: "C", Group: "g",
		Batch: &events.Batch{BatchSize: events.BatchSize{Max: 10, Wait: time.Hour}, Handle: got.handle},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publishN(t, tr, 2)
	cancel()
	drained := make(chan struct{})
	go func() {
		tr.Drain()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("Drain waited out the Wait of a cancelled subscription's batch")
	}
	if sizes := got.sizes(); len(sizes) != 1 || sizes[0] != 2 {
		t.Errorf("batch sizes = %v, want one batch of 2", sizes)
	}
}
