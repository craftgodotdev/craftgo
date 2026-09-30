package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// subscribeCoreBatch subscribes handle to orders.Placed in batches of size under group,
// on a context that ends with the test or with the returned cancel.
func subscribeCoreBatch(t *testing.T, tr *craftnats.Transport, size events.BatchSize, handle events.BatchHandler) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "bulk",
		Batch: &events.Batch{BatchSize: size, Handle: handle},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return cancel
}

func publishCore(t *testing.T, tr *craftnats.Transport, payloads ...string) {
	t.Helper()
	for _, p := range payloads {
		if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(p)}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
}

// A full batch goes at once, in publish order, and a short one once its first message has
// waited Wait.
func TestCoreNATSDeliversBatches(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn)
	seen := newBatchesSeen()
	subscribeCoreBatch(t, tr, events.BatchSize{Max: 3, Wait: 300 * time.Millisecond},
		func(_ context.Context, batch []*events.Message) error {
			seen.record(batch)
			return nil
		})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	publishCore(t, tr, "1", "2", "3", "4", "5")
	seen.await(t, 2, 10*time.Second)

	got := seen.all()
	if strings.Join(got[0], ",") != "1,2,3" || strings.Join(got[1], ",") != "4,5" {
		t.Errorf("batches = %v, want [1 2 3] then [4 5]", got)
	}
}

// A batch handler's error reaches the error handler once, with no message.
func TestCoreNATSReportsABatchErrorOnce(t *testing.T) {
	conn := runServer(t)
	reported := make(chan *events.Message, 8)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(func(_ events.Subscription, msg *events.Message, err error) {
		if err != nil && err.Error() == "store down" {
			reported <- msg
		}
	}))
	subscribeCoreBatch(t, tr, events.BatchSize{Max: 2, Wait: time.Second},
		func(context.Context, []*events.Message) error { return errors.New("store down") })
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	publishCore(t, tr, "1", "2")

	select {
	case msg := <-reported:
		if msg != nil {
			t.Errorf("the batch error arrived with message %+v, want none", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the batch error was never reported")
	}
	select {
	case <-reported:
		t.Error("one batch error was reported twice")
	case <-time.After(500 * time.Millisecond):
	}
}

// A batch subscription whose context ended gets nothing more.
func TestCoreNATSBatchStopsWithItsContext(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn)
	seen := newBatchesSeen()
	cancel := subscribeCoreBatch(t, tr, events.BatchSize{Max: 1, Wait: time.Second},
		func(_ context.Context, batch []*events.Message) error {
			seen.record(batch)
			return nil
		})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	publishCore(t, tr, "before")
	seen.await(t, 1, 10*time.Second)

	cancel()
	time.Sleep(200 * time.Millisecond)
	publishCore(t, tr, "after")
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if got := seen.all(); len(got) != 1 {
		t.Errorf("batches = %v, want only the one before the cancel", got)
	}
}
