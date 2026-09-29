package integration_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// batchesSeen collects the batches a subscription was handed, as payload strings.
type batchesSeen struct {
	mu  sync.Mutex
	got [][]string
	in  chan struct{}
}

func newBatchesSeen() *batchesSeen { return &batchesSeen{in: make(chan struct{}, 64)} }

func (b *batchesSeen) record(batch []*events.Message) {
	b.mu.Lock()
	payloads := make([]string, len(batch))
	for i, m := range batch {
		payloads[i] = string(m.Payload)
	}
	b.got = append(b.got, payloads)
	b.mu.Unlock()
	b.in <- struct{}{}
}

// await waits for n more batches.
func (b *batchesSeen) await(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for i := 0; i < n; i++ {
		select {
		case <-b.in:
		case <-deadline:
			t.Fatalf("got %d of %d batches within %s", i, n, within)
		}
	}
}

func (b *batchesSeen) all() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]string(nil), b.got...)
}

// subscribeBatch subscribes handle to orders.Placed in batches of size under group.
func subscribeBatch(t *testing.T, tr *craftnats.JetStream, group events.Group, size events.BatchSize, handle events.BatchHandler) {
	t.Helper()
	if err := tr.Subscribe(t.Context(), []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: group,
		Batch: &events.Batch{BatchSize: size, Handle: handle},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

func publishPlaced(t *testing.T, tr *craftnats.JetStream, payloads ...string) {
	t.Helper()
	for _, p := range payloads {
		if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(p)}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
}

// A full batch goes at once, in stream order, and a short one once its Wait has passed.
func TestJetStreamDeliversBatches(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	seen := newBatchesSeen()
	publishPlaced(t, tr, "1", "2", "3", "4", "5")
	subscribeBatch(t, tr, "bulk", events.BatchSize{Max: 3, Wait: 500 * time.Millisecond},
		func(_ context.Context, batch []*events.Message) error {
			seen.record(batch)
			return nil
		})
	seen.await(t, 2, 15*time.Second)

	got := seen.all()
	if strings.Join(got[0], ",") != "1,2,3" || strings.Join(got[1], ",") != "4,5" {
		t.Errorf("batches = %v, want [1 2 3] then [4 5]", got)
	}
}

// Each message of a batch gets its own answer: one redelivered comes back alone, one
// rejected never does, and the settled one is done.
func TestJetStreamAnswersEachMessageOfABatch(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	seen := newBatchesSeen()
	publishPlaced(t, tr, "redeliver", "reject", "settle")
	subscribeBatch(t, tr, "answers", events.BatchSize{Max: 3, Wait: 500 * time.Millisecond},
		func(_ context.Context, batch []*events.Message) error {
			for _, m := range batch {
				switch string(m.Payload) {
				case "redeliver":
					if m.Deliveries() == 1 {
						m.Redeliver()
					}
				case "reject":
					m.Reject()
				}
			}
			seen.record(batch)
			return nil
		})
	seen.await(t, 2, 15*time.Second)
	time.Sleep(2 * time.Second) // nothing else may come back

	got := seen.all()
	if len(got) != 2 || strings.Join(got[1], ",") != "redeliver" {
		t.Errorf("batches = %v, want the three, then the redelivered one alone", got)
	}
}

// A batch handler's error reaches the error handler once, with no message.
func TestJetStreamReportsABatchErrorOnce(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	reported := make(chan *events.Message, 8)
	tr := jsTransport(t, conn, craftnats.WithJetStreamErrorHandler(func(_ events.Subscription, msg *events.Message, err error) {
		if err != nil && err.Error() == "store down" {
			reported <- msg
		}
	}))

	publishPlaced(t, tr, "1", "2")
	subscribeBatch(t, tr, "failing", events.BatchSize{Max: 2, Wait: time.Second},
		func(context.Context, []*events.Message) error { return errors.New("store down") })

	select {
	case msg := <-reported:
		if msg != nil {
			t.Errorf("the batch error arrived with message %+v, want none", msg)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the batch error was never reported")
	}
	select {
	case <-reported:
		t.Error("one batch error was reported twice")
	case <-time.After(500 * time.Millisecond):
	}
}

// A batch handler slower than AckWait keeps its messages: an idle replica of its group
// does not get them redelivered behind it.
func TestJetStreamHoldsASlowBatch(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	other, err := natsclient.Connect(conn.ConnectedUrl())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)

	var mu sync.Mutex
	var handed []string
	inHand := make(chan struct{}, 1)
	slow := func(_ context.Context, batch []*events.Message) error {
		mu.Lock()
		for _, m := range batch {
			handed = append(handed, string(m.Payload))
		}
		mu.Unlock()
		select {
		case inHand <- struct{}{}:
		default:
		}
		time.Sleep(3 * time.Second) // three times AckWait
		return nil
	}
	first := jsTransport(t, conn, craftnats.WithAckWait(time.Second))
	publishPlaced(t, first, "1", "2")
	subscribeBatch(t, first, "slow", events.BatchSize{Max: 2, Wait: 500 * time.Millisecond}, slow)
	select {
	case <-inHand:
	case <-time.After(15 * time.Second):
		t.Fatal("no batch arrived")
	}
	subscribeBatch(t, jsTransport(t, other, craftnats.WithAckWait(time.Second)), "slow",
		events.BatchSize{Max: 2, Wait: 500 * time.Millisecond}, slow)
	time.Sleep(4 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(handed, ",") != "1,2" {
		t.Errorf("handed %v, want 1 and 2 once each - a batch slower than AckWait was redelivered to the idle replica", handed)
	}
}

// Close waits for the batch in hand, which is answered before Close returns.
func TestJetStreamCloseWaitsForTheBatchInHand(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr, err := craftnats.NewJetStream(conn)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	var finished bool
	publishPlaced(t, tr, "1")
	subscribeBatch(t, tr, "closing", events.BatchSize{Max: 1, Wait: time.Second},
		func(context.Context, []*events.Message) error {
			close(started)
			time.Sleep(time.Second)
			finished = true
			return nil
		})
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("no batch arrived")
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !finished {
		t.Error("Close returned while the batch handler was running")
	}
	if n := pending(t, conn, "closing"); n != 0 {
		t.Errorf("%d message(s) left unanswered after Close", n)
	}
}

// pending is how many messages the durable has delivered and not had answered.
func pending(t *testing.T, conn *natsclient.Conn, durable string) int {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := js.Consumer(ctx, "ORDERS", durable)
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.NumAckPending
}

// A group holding a batch subscription beside another is refused.
func TestJetStreamRefusesABatchSharingItsGroup(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	err := tr.Subscribe(t.Context(), []events.Subscription{
		{
			Event: "orders.Placed", Consumer: "C", Group: "mixed",
			Batch: &events.Batch{
				BatchSize: events.BatchSize{Max: 2, Wait: time.Second},
				Handle:    func(context.Context, []*events.Message) error { return nil },
			},
		},
		{
			Event: "orders.Shipped", Consumer: "C", Group: "mixed",
			Handle: func(context.Context, *events.Message) error { return nil },
		},
	})
	if err == nil || !strings.Contains(err.Error(), "holds its group alone") {
		t.Fatalf("subscribe = %v, want the group refused", err)
	}
}

// A batch group whose durable is deleted is reported as ErrConsumerStopped.
func TestJetStreamReportsADeletedBatchDurable(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	reported := make(chan error, 16)
	tr := jsTransport(t, conn, craftnats.WithJetStreamErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		reported <- err
	}))
	seen := newBatchesSeen()
	publishPlaced(t, tr, "1")
	subscribeBatch(t, tr, "deleted-durable", events.BatchSize{Max: 1, Wait: 2 * time.Second},
		func(_ context.Context, batch []*events.Message) error {
			seen.record(batch)
			return nil
		})
	seen.await(t, 1, 15*time.Second)

	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	durable, err := js.Consumer(ctx, "ORDERS", "deleted-durable")
	if err != nil {
		t.Fatal(err)
	}
	deleteConsumer(t, conn, durable)
	awaitConsumerStopped(t, reported, 20*time.Second)
}
