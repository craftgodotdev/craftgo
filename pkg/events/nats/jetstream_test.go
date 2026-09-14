package nats_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsclient "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// runJetStreamServer starts an in-process server WITH JetStream, so the
// adapter is exercised against a real one without Docker.
func runJetStreamServer(t *testing.T) *natsclient.Conn {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		JetStream: true, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(srv.Shutdown)

	conn, err := natsclient.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(conn.Close)
	return conn
}

// provision creates a stream covering subjects, which is the operator's
// job and never craftgo's.
func provision(t *testing.T, conn *natsclient.Conn, name string, subjects ...string) {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: subjects}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
}

// WHY THERE IS NO WithMaxProcessingTime. A bounded heartbeat would only
// stop resetting a timer, and the server does not redeliver a message
// whose delivery is still outstanding - so bounding it buys nothing and
// the option would be an escape that does not escape.
//
// Measured rather than reasoned: the handler blocks for ever, the
// heartbeat stops after 3s, AckWait is 1s. If bounding worked, a second
// delivery would arrive; it does not. This test is what makes the absence
// of that option a finding rather than an omission, and it goes red if
// the client ever changes its mind.
func TestAHungHandlerIsNotRedeliveredSoThereIsNoEscapeToShip(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Built by hand rather than through the adapter, so the heartbeat can
	// be stopped at a chosen moment - which is exactly what a
	// WithMaxProcessingTime escape would do.
	cons, err := js.CreateOrUpdateConsumer(ctx, "ORDERS", jetstream.ConsumerConfig{
		Durable:       "escape-probe",
		FilterSubject: "orders.Placed",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       time.Second,
		MaxAckPending: 64,
	})
	if err != nil {
		t.Fatal(err)
	}

	var deliveries atomic.Int64
	release := make(chan struct{})
	cc, err := cons.Consume(func(m jetstream.Msg) {
		n := deliveries.Add(1)
		if n > 1 {
			return // a redelivery arrived; nothing more to prove
		}
		// First delivery: heartbeat for 3s, then stop and block for ever.
		stop := make(chan struct{})
		go func() {
			tk := time.NewTicker(500 * time.Millisecond)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					_ = m.InProgress()
				}
			}
		}()
		time.Sleep(3 * time.Second)
		close(stop)
		<-release // hung for ever
	}, jetstream.PullMaxMessages(64))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(release); cc.Stop() }()

	if _, err := js.Publish(ctx, "orders.Placed", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-deadline:
			if got := deliveries.Load(); got != 1 {
				t.Errorf("deliveries = %d, want 1 - the client now redelivers an outstanding message, so a bounded heartbeat WOULD be an escape and the option should exist", got)
			}
			return
		case <-time.After(250 * time.Millisecond):
			if deliveries.Load() >= 2 {
				t.Fatalf("redelivered while the first delivery was outstanding - a bounded heartbeat would work after all, so WithMaxProcessingTime should ship")
			}
		}
	}
}

// jsTransport wires a JetStream adapter over a provisioned stream.
func jsTransport(t *testing.T, conn *natsclient.Conn, opts ...craftnats.JetStreamOption) *craftnats.JetStream {
	t.Helper()
	tr, err := craftnats.NewJetStream(conn, opts...)
	if err != nil {
		t.Fatalf("new jetstream: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// A published contract reaches a consumer with its key, dedup id and
// payload intact - the same wire format the core transport uses.
func TestJetStreamRoundTrip(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	got := make(chan *events.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "receipts",
		Handle: func(_ context.Context, m *events.Message) error {
			got <- m
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", DedupID: "attempt-1", Payload: []byte(`{"id":1}`),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case m := <-got:
		if m.Key != "o-1" || m.DedupID != "attempt-1" || string(m.Payload) != `{"id":1}` {
			t.Errorf("delivered %+v", m)
		}
		if m.Deliveries() != 1 {
			t.Errorf("Deliveries = %d, want 1 - the count is 1-based, as on a Kafka share group", m.Deliveries())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no delivery")
	}
}

// THE CHECK THAT EARNS THE PROBE. A consumer whose filter subject no
// stream carries is created successfully, validates, consumes
// successfully - and receives nothing for ever, with no error on any path
// at any time. Subscribe refuses instead.
func TestSubscribeRefusesASubjectNoStreamCarries(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.Subscribe(ctx, events.Subscription{
		Event: "billing.Invoiced", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	})
	if err == nil {
		t.Fatal("a subject no stream carries must be refused, not consumed silently")
	}
	for _, want := range []string{"no JetStream stream carries", "billing.Invoiced", "does not create streams"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// A server without JetStream is named as such, rather than surfacing as
// "no responders available" from a later call.
func TestSubscribeRefusesAServerWithoutJetStream(t *testing.T) {
	conn := runServer(t) // the plain server, no JetStream
	tr := jsTransport(t, conn, craftnats.WithProbeTimeout(2*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	})
	if err == nil {
		t.Fatal("a server without JetStream must be refused")
	}
	if !strings.Contains(err.Error(), "JetStream is not available") {
		t.Errorf("refusal does not name the cause: %v", err)
	}
	// The timeout is the other way a cluster answers, so the message has
	// to say so or an operator reads a timeout as a network problem.
	if !strings.Contains(err.Error(), "does not answer at all") {
		t.Errorf("refusal does not explain the timeout case: %v", err)
	}
}

// Redeliver brings the SAME message back and Reject gives it up - which
// is the whole reason this adapter exists beside the core one.
func TestJetStreamRedeliversAndRejects(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithAckWait(2*time.Second))

	var (
		mu   sync.Mutex
		seen []int
	)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "redeliver",
		Handle: func(_ context.Context, m *events.Message) error {
			mu.Lock()
			seen = append(seen, m.Deliveries())
			n := len(seen)
			mu.Unlock()
			if n == 1 {
				m.Redeliver()
				return nil
			}
			m.Reject()
			close(done)
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the message was not redelivered")
	}
	time.Sleep(3 * time.Second) // a rejected message must not come back

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("deliveries = %v, want exactly two - redeliver once, then reject", seen)
	}
	if seen[0] != 1 || seen[1] != 2 {
		t.Errorf("delivery counts = %v, want [1 2]", seen)
	}
}

// A middleware that keeps asking for a message nothing can handle is a
// loop, and the cap is what ends it. Measured without one: the same
// message passes 200,000 deliveries inside ten seconds.
func TestMaxDeliveriesTerminatesARedeliveryLoop(t *testing.T) {
	const cap = 3
	seen := redeliverForever(t, "capped", craftnats.WithMaxDeliveries(cap))

	seen.waitFor(t, cap, 20*time.Second)
	time.Sleep(3 * time.Second) // a terminated message must not come back
	if got := seen.counts(); len(got) != cap {
		t.Fatalf("%d deliveries, want %d - the cap has to end the loop", len(got), cap)
	} else if got[0] != 1 || got[cap-1] != cap {
		t.Errorf("delivery counts = %v, want 1..%d", got, cap)
	}
}

// The cap is a guard a middleware author can forget, so it applies
// without being asked for. Five, the same as the Kafka adapter's.
func TestTheDefaultMaxDeliveriesIsFive(t *testing.T) {
	seen := redeliverForever(t, "default")

	seen.waitFor(t, 5, 20*time.Second)
	time.Sleep(3 * time.Second)
	if got := seen.counts(); len(got) != 5 {
		t.Fatalf("%d deliveries, want 5 - the default cap has to apply on its own", len(got))
	}
}

// Zero is unbounded, which is what makes the default a decision rather
// than a ceiling nobody chose. The same loop keeps going well past where
// the default would have stopped it.
func TestMaxDeliveriesZeroIsUnbounded(t *testing.T) {
	seen := redeliverForever(t, "uncapped", craftnats.WithMaxDeliveries(0))
	seen.waitFor(t, 50, 20*time.Second)
}

// redeliverForever runs one message through a handler that always fails
// and a middleware that always asks for it back, and reports every
// delivery the server made.
func redeliverForever(t *testing.T, group string, opts ...craftnats.JetStreamOption) *attempts {
	t.Helper()
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, append(opts, craftnats.WithAckWait(2*time.Second))...)

	seen := &attempts{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: group,
		Handle: func(_ context.Context, m *events.Message) error {
			seen.add(m.Deliveries())
			m.Redeliver() // never gives up
			return errors.New("nothing can handle this")
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	return seen
}

// attempts is the delivery count of every delivery, in order.
type attempts struct {
	mu  sync.Mutex
	got []int
}

func (a *attempts) add(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, n)
}

func (a *attempts) counts() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.got...)
}

func (a *attempts) waitFor(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if got := a.counts(); len(got) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d deliveries after %s, want %d", len(a.counts()), within, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// THE HEARTBEAT'S VALUE. A handler slower than AckWait is not redelivered
// behind itself: the message is held open while it runs. Without it this
// is a duplicate roughly one run in ten, which is the worst kind of bug -
// invisible and nondeterministic.
func TestASlowHandlerIsNotRedeliveredBehindItself(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithAckWait(time.Second))

	var deliveries atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "slow",
		Handle: func(context.Context, *events.Message) error {
			deliveries.Add(1)
			time.Sleep(4 * time.Second) // four times AckWait
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(9 * time.Second)
	if got := deliveries.Load(); got != 1 {
		t.Errorf("the handler ran %d times for one message - a handler slower than AckWait was redelivered behind itself", got)
	}
}

// The JetStream transport can do what the core one cannot, and says so.
// The answer is a constant because the bus asks it BEFORE the transport
// subscribes - the consumer it would ask about does not exist yet.
func TestJetStreamCanDisposition(t *testing.T) {
	tr, err := craftnats.NewJetStream(runJetStreamServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()
	for _, d := range []events.Disposition{
		events.DispositionSettle, events.DispositionRedeliver, events.DispositionReject,
	} {
		if !tr.CanDisposition(d) {
			t.Errorf("JetStream claims it cannot %v", d)
		}
	}
	if tr.CanDisposition(events.DispositionUnset) {
		t.Error("unset is not something a transport honours")
	}
}

// A middleware reaching for the stream sequence gets the JetStream
// message - and the CORE transport's accessor does not find it, because a
// JetStream delivery is not a *nats.Msg.
func TestTheJetStreamMessageIsReachableAndIsNotACoreMessage(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	type seen struct {
		streamSeq uint64
		foundJS   bool
		foundCore bool
	}
	got := make(chan seen, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "raw",
		Handle: func(hctx context.Context, _ *events.Message) error {
			var s seen
			if m, ok := craftnats.JetStreamMsgFrom(hctx); ok {
				s.foundJS = true
				if meta, err := m.Metadata(); err == nil {
					s.streamSeq = meta.Sequence.Stream
				}
			}
			_, s.foundCore = craftnats.MsgFrom(hctx)
			got <- s
			return nil
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case s := <-got:
		if !s.foundJS {
			t.Error("JetStreamMsgFrom found nothing on a JetStream delivery")
		}
		if s.streamSeq == 0 {
			t.Error("the stream sequence is not reachable - that is the point of the accessor")
		}
		if s.foundCore {
			t.Error("MsgFrom found a core message on a JetStream delivery - the two keys must not collide")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no delivery")
	}
}

// A batch reaches the stream, and a message the stream will not take is
// named by index rather than by a count.
func TestJetStreamPublishBatch(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	var got atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "batch",
		Handle: func(context.Context, *events.Message) error {
			got.Add(1)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	msgs := []*events.Message{
		{Event: "orders.Placed", Key: "a", Payload: []byte(`{}`)},
		{Event: "orders.Placed", Key: "b", Payload: []byte(`{}`)},
		{Event: "orders.Placed", Key: "c", Payload: []byte(`{}`)},
	}
	if err := tr.PublishBatch(context.Background(), msgs); err != nil {
		t.Fatalf("publish batch: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for got.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("delivered %d of 3", got.Load())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// A batch whose subject no stream carries names every message as unsent
// rather than reporting a count that reads as partial success.
func TestAJetStreamBatchToNoStreamIsAllUnsent(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	msgs := []*events.Message{
		{Event: "billing.One", Payload: []byte(`{}`)},
		{Event: "billing.Two", Payload: []byte(`{}`)},
	}
	err := tr.PublishBatch(context.Background(), msgs)
	if err == nil {
		t.Fatal("a batch no stream carries must fail")
	}
	var partial *events.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %T %v, want *PartialPublishError", err, err)
	}
	if len(partial.Unsent) != 2 {
		t.Errorf("Unsent = %v, want both indices - nothing was stored", partial.Unsent)
	}
}

// When the cap overrides a redelivery the chain asked for, the message is
// terminated and the chain never hears about it: its dead-letter
// middleware sees a message on its way back, not one given up, so it
// writes no record. The transport's error handler is the only layer left
// that can say the message is gone.
func TestTheDeliveryCapReportsThatItFired(t *testing.T) {
	const cap = 2
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	var (
		mu   sync.Mutex
		errs []error
		keys []string
	)
	tr := jsTransport(t, conn, craftnats.WithMaxDeliveries(cap), craftnats.WithAckWait(2*time.Second),
		craftnats.WithJetStreamErrorHandler(func(_ events.Subscription, msg *events.Message, err error) {
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
			if msg != nil {
				keys = append(keys, msg.Key)
			}
		}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "capped-report",
		Handle: func(_ context.Context, m *events.Message) error {
			m.Redeliver() // never gives up
			return nil    // and never fails, so nothing else reports
		},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		mu.Lock()
		got := len(errs)
		mu.Unlock()
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cap fired and nothing was reported")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if n := len(errs); n != 1 {
		t.Fatalf("%d reports, want 1", n)
	}
	text := errs[0].Error()
	for _, want := range []string{"orders.Placed", "2 deliveries", "WithMaxDeliveries is 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("report %q does not name %q", text, want)
		}
	}
	if len(keys) != 1 || keys[0] != "o-1" {
		t.Errorf("the report does not carry the message it is about: %v", keys)
	}
}

// storedIn is how many messages a stream actually holds.
func storedIn(t *testing.T, conn *natsclient.Conn, name string) uint64 {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := js.Stream(ctx, name)
	if err != nil {
		t.Fatalf("stream %s: %v", name, err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}

// A context already cancelled sends nothing, and says so as a plain error
// rather than a partial report.
//
// The async publish takes no context, so a batch that got as far as the
// publish loop would be stored in full and then reported unsent - and a
// caller retrying what it was told never went out publishes the whole
// batch a second time.
func TestPublishBatchRefusesAnAlreadyCancelledContext(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	msgs := []*events.Message{
		{Event: "orders.Placed", Payload: []byte(`{}`)},
		{Event: "orders.Paid", Payload: []byte(`{}`)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := tr.PublishBatch(ctx, msgs)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var partial *events.PartialPublishError
	if errors.As(err, &partial) {
		t.Errorf("err is a partial report (%v), but nothing was published", partial)
	}
	time.Sleep(300 * time.Millisecond)
	if n := storedIn(t, conn, "ORDERS"); n != 0 {
		t.Errorf("stream holds %d messages, want 0 - a refused batch must not reach the wire", n)
	}
}

// A cancellation arriving once the batch is on the wire does not cut the
// wait short: every message is still waited for and reported by its own
// acknowledgement.
//
// The subject mapping cancels while the publish loop runs, so the context
// is certainly done before the first wait begins.
func TestACancellationAfterThePublishDoesNotAbandonTheAcks(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := jsTransport(t, conn, craftnats.WithJetStreamSubject(func(c string) string {
		if c == "orders.Last" {
			cancel()
		}
		return c
	}))

	msgs := []*events.Message{
		{Event: "orders.Placed", Payload: []byte(`{}`)},
		{Event: "orders.Paid", Payload: []byte(`{}`)},
		{Event: "orders.Last", Payload: []byte(`{}`)},
	}
	if err := tr.PublishBatch(ctx, msgs); err != nil {
		t.Fatalf("publish batch: %v - the stream stored these, so reporting them unsent duplicates them on retry", err)
	}
	if n := storedIn(t, conn, "ORDERS"); n != 3 {
		t.Errorf("stream holds %d messages, want 3", n)
	}
}

// A message the client refuses to publish stops the batch, and the ones
// already in flight are still waited for rather than claimed as sent.
//
// Their acknowledgements are the only evidence they landed. Reporting
// them sent without it loses exactly the ones that did not, and nothing
// downstream can tell that happened.
func TestPublishBatchDoesNotClaimAnUnwaitedMessageWasSent(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	// The empty subject is one the client rejects outright, so the batch
	// stops at index 1 while index 0 is still in flight - to a subject no
	// stream carries, so it is never stored.
	tr := jsTransport(t, conn, craftnats.WithJetStreamSubject(func(c string) string {
		if c == "orders.Unroutable" {
			return ""
		}
		return c
	}))

	msgs := []*events.Message{
		{Event: "billing.One", Payload: []byte(`{}`)},
		{Event: "orders.Unroutable", Payload: []byte(`{}`)},
		{Event: "orders.Placed", Payload: []byte(`{}`)},
	}
	err := tr.PublishBatch(context.Background(), msgs)

	var partial *events.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %T %v, want *PartialPublishError", err, err)
	}
	if len(partial.Unsent) != 3 {
		t.Errorf("Unsent = %v, want all three - none of them is stored", partial.Unsent)
	}
	if partial.Sent != 0 {
		t.Errorf("Sent = %d, want 0", partial.Sent)
	}
	if n := storedIn(t, conn, "ORDERS"); n != 0 {
		t.Errorf("stream holds %d messages, want 0", n)
	}
}

// A batch fails only the messages no stream carries, and the ones around
// them still land.
//
// This is why the report is built from a set of indices: a batch spans
// contracts, contracts map to different subjects, and a failure in the
// middle of one is not a tail.
func TestAJetStreamBatchFailsOnlyTheMessagesNoStreamCarries(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	msgs := []*events.Message{
		{Event: "orders.Placed", Payload: []byte(`{}`)},
		{Event: "billing.One", Payload: []byte(`{}`)},
		{Event: "orders.Paid", Payload: []byte(`{}`)},
	}
	err := tr.PublishBatch(context.Background(), msgs)

	var partial *events.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %T %v, want *PartialPublishError", err, err)
	}
	if len(partial.Unsent) != 1 || partial.Unsent[0] != 1 {
		t.Errorf("Unsent = %v, want [1] - the messages either side of it are stored", partial.Unsent)
	}
	if partial.Sent != 1 {
		t.Errorf("Sent = %d, want 1", partial.Sent)
	}
	if n := storedIn(t, conn, "ORDERS"); n != 2 {
		t.Errorf("stream holds %d messages, want 2", n)
	}
}

// An acknowledgement that never arrives resolves as that message's own
// error, so a stream that does not answer fails the batch instead of
// holding the caller for ever. The caller's context is not the bound.
//
// The stream is configured not to acknowledge, which stores the message
// and replies to nobody. The report calls it unsent even though it
// landed: an outcome the adapter could not learn counts as unsent, and
// this is what that costs.
func TestPublishBatchGivesUpOnAnAcknowledgementThatNeverComes(t *testing.T) {
	conn := runJetStreamServer(t)
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: "QUIET", Subjects: []string{"orders.>"}, NoAck: true,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
	tr := jsTransport(t, conn, craftnats.WithPublishAckTimeout(500*time.Millisecond))

	msgs := []*events.Message{{Event: "orders.Placed", Payload: []byte(`{}`)}}
	done := make(chan error, 1)
	go func() { done <- tr.PublishBatch(context.Background(), msgs) }()

	select {
	case err := <-done:
		var partial *events.PartialPublishError
		if !errors.As(err, &partial) {
			t.Fatalf("err = %T %v, want *PartialPublishError", err, err)
		}
		if len(partial.Unsent) != 1 {
			t.Errorf("Unsent = %v, want [0]", partial.Unsent)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("PublishBatch never returned - the ack timeout is the only bound on the wait")
	}
}

// quietStream provisions a stream that stores messages and acknowledges
// none of them, so a publish waits for a verdict that never comes.
func quietStream(t *testing.T, conn *natsclient.Conn, name string, subjects ...string) {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: name, Subjects: subjects, NoAck: true,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}
}

// Close ends a publish that is waiting, rather than leaving it to run out
// the ack timeout.
//
// Without this the only bound is [craftnats.WithPublishAckTimeout], so a
// shutdown waits out a timeout chosen to be far longer than any healthy
// ack - the caller is held long after the transport it is publishing
// through has gone.
func TestCloseEndsAPublishThatIsWaiting(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn, "QUIET", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithPublishAckTimeout(60*time.Second))

	msgs := []*events.Message{
		{Event: "orders.Placed", Payload: []byte(`{}`)},
		{Event: "orders.Paid", Payload: []byte(`{}`)},
	}
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- tr.PublishBatch(context.Background(), msgs) }()

	time.Sleep(300 * time.Millisecond)
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Errorf("the publish took %s to return - it waited out the ack timeout instead of the close", elapsed)
		}
		if !errors.Is(err, craftnats.ErrClosed) {
			t.Fatalf("err = %v, want ErrClosed", err)
		}
		var partial *events.PartialPublishError
		if !errors.As(err, &partial) {
			t.Fatalf("err = %T %v, want *PartialPublishError", err, err)
		}
		if len(partial.Unsent) != 2 {
			t.Errorf("Unsent = %v, want both - their verdicts can no longer arrive", partial.Unsent)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not end the publish")
	}
}

// A batch that starts after Close is refused, and reaches the broker not
// at all.
//
// The wait alone would not be enough: every message would be handed over
// first and then named unsent on the first turn of the loop, which is a
// batch genuinely sent and entirely reported for retry.
func TestPublishBatchAfterCloseSendsNothing(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	msgs := []*events.Message{
		{Event: "orders.Placed", Payload: []byte(`{}`)},
		{Event: "orders.Paid", Payload: []byte(`{}`)},
	}
	err := tr.PublishBatch(context.Background(), msgs)
	if !errors.Is(err, craftnats.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := storedIn(t, conn, "ORDERS"); n != 0 {
		t.Errorf("stream holds %d messages, want 0 - a refused batch must not reach the wire", n)
	}
}

// A negative ack timeout is refused at construction. The client takes it
// the way it takes zero - no timer at all - so it would wait for ever.
func TestANegativePublishAckTimeoutIsRefused(t *testing.T) {
	conn := runJetStreamServer(t)
	_, err := craftnats.NewJetStream(conn, craftnats.WithPublishAckTimeout(-time.Second))
	if err == nil {
		t.Fatal("a negative ack timeout must be refused")
	}
	for _, want := range []string{"negative", "wait for ever"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// One number bounds both publish paths: the synchronous publish takes the
// same deadline as the batch wait when the caller brings none of its own.
func TestThePublishAckTimeoutBoundsTheSynchronousPublishToo(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn, "QUIET", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithPublishAckTimeout(700*time.Millisecond))

	started := time.Now()
	err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("a publish nobody acknowledged must fail")
	}
	// The client's own default is 5s; this has to be the adapter's number.
	if elapsed > 3*time.Second {
		t.Errorf("publish took %s - it is bounded by the client's default, not WithPublishAckTimeout", elapsed)
	}
}
