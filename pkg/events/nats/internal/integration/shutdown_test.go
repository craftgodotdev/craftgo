package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// subscribeAgain subscribes subs on ctx once the group has stopped consuming under the
// subscription before it.
func subscribeAgain(t *testing.T, ctx context.Context, tr *craftnats.JetStream, subs ...events.Subscription) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := tr.Subscribe(ctx, subs)
		if err == nil {
			return
		}
		if !strings.Contains(err.Error(), "still stopping") || time.Now().After(deadline) {
			t.Fatalf("subscribe again: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A message whose handler the shutdown cut short goes back to the stream at once and comes
// to the next consumer of its group; a handler error with the subscription live settles it.
func TestJetStreamHandsBackAMessageItsShutdownCutShort(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	// Far longer than the test: a redelivery within it is the hand-back, not AckWait.
	tr := jsTransport(t, conn, craftnats.WithAckWait(time.Minute))

	started := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "cut-short",
		Handle: func(ctx context.Context, _ *events.Message) error {
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publishPlaced(t, tr, "cut")
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("no delivery")
	}
	cancel()

	again := make(chan int, 4)
	subscribeAgain(t, t.Context(), tr, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "cut-short",
		Handle: func(_ context.Context, m *events.Message) error {
			again <- m.Deliveries()
			return nil
		},
	})
	select {
	case n := <-again:
		if n != 2 {
			t.Errorf("redelivered as delivery %d, want 2", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the message cut short by the shutdown did not come back")
	}
}

// A handler error while the subscription is live settles the message: it does not come back.
func TestJetStreamSettlesAFailureOutsideAShutdown(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithAckWait(2*time.Second))

	seen := make(chan int, 4)
	if err := tr.Subscribe(t.Context(), []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "failing",
		Handle: func(_ context.Context, m *events.Message) error {
			seen <- m.Deliveries()
			return context.DeadlineExceeded
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publishPlaced(t, tr, "fails")
	select {
	case <-seen:
	case <-time.After(15 * time.Second):
		t.Fatal("no delivery")
	}
	select {
	case n := <-seen:
		t.Errorf("the message came back as delivery %d - an error with nothing asked settles it", n)
	case <-time.After(4 * time.Second):
	}
}

// A batch the shutdown cut short goes back to the stream at once, every message of it.
func TestJetStreamHandsBackABatchItsShutdownCutShort(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithAckWait(time.Minute))

	started := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publishPlaced(t, tr, "1", "2", "3")
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "cut-short-batch",
		Batch: &events.Batch{BatchSize: events.BatchSize{Max: 10, Wait: 300 * time.Millisecond},
			Handle: func(ctx context.Context, _ []*events.Message) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("no batch")
	}
	cancel()

	seen := newBatchesSeen()
	subscribeAgain(t, t.Context(), tr, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "cut-short-batch",
		Batch: &events.Batch{BatchSize: events.BatchSize{Max: 10, Wait: 300 * time.Millisecond},
			Handle: func(_ context.Context, batch []*events.Message) error {
				seen.record(batch)
				return nil
			}},
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		n := 0
		for _, b := range seen.all() {
			n += len(b)
		}
		if n == 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("got back %v, want the 3 messages of the batch cut short", seen.all())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
