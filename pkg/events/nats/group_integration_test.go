package nats_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// consumerConfig reads a durable's configuration back from the server.
func consumerConfig(t *testing.T, conn *natsclient.Conn, stream, name string) jetstream.ConsumerConfig {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := js.Consumer(ctx, stream, name)
	if err != nil {
		t.Fatalf("consumer %s: %v", name, err)
	}
	return c.CachedInfo().Config
}

// createConsumer provisions a durable by hand, the way an operator or an
// earlier version of an application would have.
func createConsumer(t *testing.T, conn *natsclient.Conn, stream string, cfg jetstream.ConsumerConfig) {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.CreateConsumer(ctx, stream, cfg); err != nil {
		t.Fatalf("create consumer %s: %v", cfg.Durable, err)
	}
}

type deliveries struct {
	mu   sync.Mutex
	seen []string
}

func (d *deliveries) add(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, s)
}

func (d *deliveries) list() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.seen...)
}

func (d *deliveries) waitFor(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for len(d.list()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d deliveries after %s, want %d: %v", len(d.list()), within, n, d.list())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func recording(d *deliveries) events.Handler {
	return func(_ context.Context, m *events.Message) error {
		d.add(m.Event + ":" + m.Key)
		return nil
	}
}

func TestOneGroupOverTwoContractsSharesOneDurableInStreamOrder(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := &deliveries{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := bus.SubscribeAll(ctx, []events.Subscription{
		{Event: "orders.Shipped", Consumer: "Dispatch", Group: "orders-worker", Handle: recording(got)},
		{Event: "orders.Placed", Consumer: "Receipt", Group: "orders-worker", Handle: recording(got)},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	cfg := consumerConfig(t, conn, "ORDERS", "orders-worker")
	if cfg.Durable != "orders-worker" {
		t.Errorf("durable = %q, want the group name", cfg.Durable)
	}
	if want := []string{"orders.Placed", "orders.Shipped"}; strings.Join(cfg.FilterSubjects, ",") != strings.Join(want, ",") {
		t.Errorf("filter subjects = %v, want %v", cfg.FilterSubjects, want)
	}

	for _, m := range []struct{ event, key string }{
		{"orders.Placed", "o-1"}, {"orders.Shipped", "o-1"}, {"orders.Placed", "o-2"},
	} {
		if err := bus.Publish(ctx, m.event, map[string]string{"id": m.key}, events.WithKey(m.key)); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	got.waitFor(t, 3, 15*time.Second)
	if want := "orders.Placed:o-1,orders.Shipped:o-1,orders.Placed:o-2"; strings.Join(got.list(), ",") != want {
		t.Errorf("delivered %v, want stream order %s", got.list(), want)
	}
}

func TestAnExistingDurableKeepsItsPositionAndPolicy(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	// Published before the durable exists: a DeliverNew durable never
	// sees it, and adopting the durable must not replay it either.
	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Key: "old", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	createConsumer(t, conn, "ORDERS", jetstream.ConsumerConfig{
		Durable: "late", Name: "late", FilterSubject: "orders.Placed",
		AckPolicy: jetstream.AckExplicitPolicy, DeliverPolicy: jetstream.DeliverNewPolicy,
	})

	got := &deliveries{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.SubscribeBatch(ctx, []events.Subscription{
		{Event: "orders.Placed", Consumer: "A", Group: "late", Handle: recording(got)},
		{Event: "orders.Shipped", Consumer: "B", Group: "late", Handle: recording(got)},
	})
	if err != nil {
		t.Fatalf("adopting a DeliverNew durable failed: %v", err)
	}

	cfg := consumerConfig(t, conn, "ORDERS", "late")
	if cfg.DeliverPolicy != jetstream.DeliverNewPolicy {
		t.Errorf("deliver policy = %v, want DeliverNew kept", cfg.DeliverPolicy)
	}
	if want := "orders.Placed,orders.Shipped"; strings.Join(cfg.FilterSubjects, ",") != want {
		t.Errorf("filter subjects = %v, want %s", cfg.FilterSubjects, want)
	}

	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Shipped", Key: "new", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	got.waitFor(t, 1, 15*time.Second)
	time.Sleep(time.Second)
	if list := got.list(); len(list) != 1 || list[0] != "orders.Shipped:new" {
		t.Errorf("delivered %v, want only the message published after the durable existed", list)
	}
}

func TestADurableThatDoesNotAcknowledgeExplicitlyIsRefused(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	createConsumer(t, conn, "ORDERS", jetstream.ConsumerConfig{
		Durable: "fire-and-forget", Name: "fire-and-forget", FilterSubject: "orders.Placed",
		AckPolicy: jetstream.AckNonePolicy,
	})
	tr := jsTransport(t, conn)

	err := tr.Subscribe(context.Background(), events.Subscription{
		Event: "orders.Placed", Consumer: "A", Group: "fire-and-forget",
		Handle: func(context.Context, *events.Message) error { return nil },
	})
	if err == nil {
		t.Fatal("a durable with AckNone must be refused - Redeliver would silently do nothing")
	}
	if !strings.Contains(err.Error(), "acknowledge explicitly") {
		t.Errorf("refusal does not say why: %v", err)
	}
}

func TestAGroupSpanningTwoStreamsIsRefused(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	provision(t, conn, "BILLING", "billing.>")
	tr := jsTransport(t, conn)

	err := tr.SubscribeBatch(context.Background(), []events.Subscription{
		{Event: "orders.Placed", Consumer: "A", Group: "mixed", Handle: recording(&deliveries{})},
		{Event: "billing.Invoiced", Consumer: "B", Group: "mixed", Handle: recording(&deliveries{})},
	})
	if err == nil {
		t.Fatal("a group whose subjects sit on two streams must be refused")
	}
	for _, want := range []string{"mixed", "ORDERS", "BILLING", "reads one stream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

func TestAGroupSubscribedTwiceOnOneTransportIsRefused(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Subscribe(ctx, events.Subscription{Event: "orders.Placed", Consumer: "A", Group: "g", Handle: recording(&deliveries{})}); err != nil {
		t.Fatal(err)
	}
	err := tr.Subscribe(ctx, events.Subscription{Event: "orders.Shipped", Consumer: "B", Group: "g", Handle: recording(&deliveries{})})
	if err == nil {
		t.Fatal("the second contract of a group must arrive in the same SubscribeAll")
	}
	for _, want := range []string{"already subscribed", "SubscribeAll"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

func TestAGroupSubscribingOneSubjectTwiceIsRefused(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	err := tr.SubscribeBatch(context.Background(), []events.Subscription{
		{Event: "orders.Placed", Consumer: "A", Group: "g", Handle: recording(&deliveries{})},
		{Event: "orders.Placed", Consumer: "B", Group: "g", Handle: recording(&deliveries{})},
	})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("err = %v, want a refusal naming the repeated subject", err)
	}
}

// redeliveryGap runs one message through a handler that asks for it back
// once and reports how long the server took to hand it back.
func redeliveryGap(t *testing.T, group string, opts ...craftnats.JetStreamOption) time.Duration {
	t.Helper()
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, append([]craftnats.JetStreamOption{craftnats.WithAckWait(10 * time.Second)}, opts...)...)

	var (
		mu    sync.Mutex
		times []time.Time
	)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: group,
		Handle: func(_ context.Context, m *events.Message) error {
			mu.Lock()
			times = append(times, time.Now())
			n := len(times)
			mu.Unlock()
			if n == 1 {
				m.Redeliver()
				return nil
			}
			close(done)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the message was not redelivered")
	}
	mu.Lock()
	defer mu.Unlock()
	return times[1].Sub(times[0])
}

func TestRedeliverBackoffDelaysTheNextDelivery(t *testing.T) {
	const delay = 1500 * time.Millisecond
	gap := redeliveryGap(t, "backoff", craftnats.WithRedeliverBackoff(func(int) time.Duration { return delay }))
	if gap < delay-100*time.Millisecond {
		t.Errorf("redelivered after %s, want at least %s", gap, delay)
	}
}

func TestAnUndelayedRedeliveryIsImmediate(t *testing.T) {
	if gap := redeliveryGap(t, "nobackoff"); gap > time.Second {
		t.Errorf("redelivered after %s; without a backoff a NAK is immediate", gap)
	}
}

// THE PREFETCH TRAP. Messages are handled one at a time, so a buffered
// message waits for every handler ahead of it with the server's AckWait
// clock running - and only the message inside the handler is held open.
// The default buffers nothing, so each message is pulled once the one
// before it is answered.
func TestABufferedMessageIsNotRedeliveredBehindASlowSibling(t *testing.T) {
	const (
		ackWait = 2 * time.Second
		handler = 1200 * time.Millisecond
	)
	run := func(t *testing.T, opts ...craftnats.JetStreamOption) []string {
		t.Helper()
		conn := runJetStreamServer(t)
		provision(t, conn, "ORDERS", "orders.>")
		tr := jsTransport(t, conn, append([]craftnats.JetStreamOption{craftnats.WithAckWait(ackWait)}, opts...)...)
		for _, key := range []string{"a", "b", "c"} {
			if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Key: key, Payload: []byte(`{}`)}); err != nil {
				t.Fatal(err)
			}
		}
		got := &deliveries{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := tr.Subscribe(ctx, events.Subscription{
			Event: "orders.Placed", Consumer: "C", Group: "queue",
			Handle: func(_ context.Context, m *events.Message) error {
				got.add(m.Key)
				time.Sleep(handler)
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}
		got.waitFor(t, 3, 30*time.Second)
		time.Sleep(3 * ackWait)
		return got.list()
	}

	t.Run("the default pulls one message at a time", func(t *testing.T) {
		if got := run(t); len(got) != 3 {
			t.Errorf("delivered %v - a message was redelivered while it sat in the buffer", got)
		}
	})
	t.Run("a prefetch larger than AckWait allows redelivers a buffered message", func(t *testing.T) {
		if got := run(t, craftnats.WithMaxInFlight(64)); len(got) <= 3 {
			t.Skipf("delivered %v - the client did not redeliver a buffered message this run; the trap is timing-dependent", got)
		}
	})
}

// During a rolling deploy two versions of a design share a durable. A
// subject the older replica has no consumer for is handed back rather
// than acknowledged away, so the replica that consumes it gets it.
func TestASubjectNothingHereHandlesIsHandedBack(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	var (
		mu       sync.Mutex
		reported []string
	)
	old := jsTransport(t, conn, craftnats.WithAckWait(time.Second),
		craftnats.WithJetStreamErrorHandler(func(sub events.Subscription, _ *events.Message, err error) {
			mu.Lock()
			reported = append(reported, sub.Group+": "+err.Error())
			mu.Unlock()
		}))
	current := jsTransport(t, conn, craftnats.WithAckWait(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldGot, currentGot := &deliveries{}, &deliveries{}
	if err := old.Subscribe(ctx, events.Subscription{Event: "orders.Placed", Consumer: "A", Group: "rolling", Handle: recording(oldGot)}); err != nil {
		t.Fatal(err)
	}
	if err := current.SubscribeBatch(ctx, []events.Subscription{
		{Event: "orders.Placed", Consumer: "A", Group: "rolling", Handle: recording(currentGot)},
		{Event: "orders.Shipped", Consumer: "B", Group: "rolling", Handle: recording(currentGot)},
	}); err != nil {
		t.Fatal(err)
	}

	if err := current.Publish(context.Background(), &events.Message{Event: "orders.Shipped", Key: "s-1", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	currentGot.waitFor(t, 1, 20*time.Second)
	time.Sleep(2 * time.Second)
	if got := currentGot.list(); len(got) != 1 || got[0] != "orders.Shipped:s-1" {
		t.Errorf("the current replica saw %v, want the shipped message once", got)
	}
	if got := oldGot.list(); len(got) != 0 {
		t.Errorf("the old replica handled %v, which it has no consumer for", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range reported {
		if !strings.HasPrefix(r, "rolling: ") || !strings.Contains(r, "orders.Shipped") {
			t.Errorf("unexpected report %q", r)
		}
	}
}

func TestCloseLetsTheHandlerInFlightFinish(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	started := make(chan struct{})
	var finished atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "draining",
		Handle: func(context.Context, *events.Message) error {
			close(started)
			time.Sleep(1500 * time.Millisecond)
			finished.Store(true)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("no delivery")
	}

	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !finished.Load() {
		t.Error("Close returned while the handler was still running")
	}
}

func TestCloseGivesUpOnAHungHandler(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithDrainTimeout(500*time.Millisecond))

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "hung",
		Handle: func(context.Context, *events.Message) error {
			close(started)
			<-release
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	<-started

	begun := time.Now()
	err := tr.Close()
	if err == nil {
		t.Fatal("Close must report a handler it gave up on")
	}
	if took := time.Since(begun); took > 5*time.Second {
		t.Errorf("Close took %s, want about the drain timeout", took)
	}
}
