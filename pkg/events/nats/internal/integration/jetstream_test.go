package integration_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsclient "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// runJetStreamServer starts an in-process server with JetStream and connects.
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

// provision creates a stream covering subjects, as an operator would.
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

// An outstanding delivery is not redelivered after its heartbeat stops.
func TestAHungHandlerIsNotRedeliveredSoThereIsNoEscapeToShip(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Built by hand so the heartbeat can be stopped at a chosen moment.
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

// jsTransport builds a JetStream transport that is closed at cleanup.
func jsTransport(t *testing.T, conn *natsclient.Conn, opts ...craftnats.JetStreamOption) *craftnats.JetStream {
	t.Helper()
	tr, err := craftnats.NewJetStream(conn, opts...)
	if err != nil {
		t.Fatalf("new jetstream: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// A message reaches its consumer with its key, dedup ID and payload intact.
func TestJetStreamRoundTrip(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	got := make(chan *events.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "receipts",
		Handle: func(_ context.Context, m *events.Message) error {
			got <- m
			return nil
		},
	}}); err != nil {
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

func TestSubscribeRefusesASubjectNoStreamCarries(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "billing.Invoiced", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}})
	if err == nil {
		t.Fatal("a subject no stream carries must be refused, not consumed silently")
	}
	for _, want := range []string{"no JetStream stream carries", "billing.Invoiced", "does not create streams"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

func TestSubscribeRefusesAServerWithoutJetStream(t *testing.T) {
	conn := runServer(t) // the plain server, no JetStream
	tr := jsTransport(t, conn, craftnats.WithProbeTimeout(2*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}})
	if err == nil {
		t.Fatal("a server without JetStream must be refused")
	}
	if !strings.Contains(err.Error(), "JetStream is not available") {
		t.Errorf("refusal does not name the cause: %v", err)
	}
	// A clustered server answers only by timing out, so the refusal says so.
	if !strings.Contains(err.Error(), "does not answer at all") {
		t.Errorf("refusal does not explain the timeout case: %v", err)
	}
}

// Redeliver brings the same message back and Reject gives it up.
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
	if err := tr.Subscribe(ctx, []events.Subscription{{
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
	}}); err != nil {
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

// A panic in a bus middleware naks the message, so it is delivered again.
func TestAPanicInAMiddlewareIsNakkedRatherThanAcked(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	var (
		mu       sync.Mutex
		seen     []int
		reported []error
	)
	tr := jsTransport(t, conn,
		craftnats.WithAckWait(2*time.Second),
		craftnats.WithJetStreamErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
			mu.Lock()
			reported = append(reported, err)
			mu.Unlock()
		}))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	done := make(chan struct{})
	bus.Use(func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, m *events.Message) error {
			mu.Lock()
			seen = append(seen, m.Deliveries())
			first := len(seen) == 1
			mu.Unlock()
			if first {
				panic("middleware blew up")
			}
			close(done)
			return next(ctx, m)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Register(events.Subscription{
		Event: "orders.Placed", Consumer: "C", Group: "escaped-panic",
		Handle: func(context.Context, *events.Message) error { return nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the message was acked and never came back - a panic in a middleware lost it")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("deliveries = %v, want exactly two - the panic hands the message back once", seen)
	}
	if seen[0] != 1 || seen[1] != 2 {
		t.Errorf("delivery counts = %v, want [1 2]", seen)
	}
	var panicked *events.PanicError
	if len(reported) == 0 || !errors.As(reported[0], &panicked) {
		t.Fatalf("the adapter reported %v, want a *PanicError", reported)
	}
}

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

func TestTheDefaultMaxDeliveriesIsFive(t *testing.T) {
	seen := redeliverForever(t, "default")

	seen.waitFor(t, 5, 20*time.Second)
	time.Sleep(3 * time.Second)
	if got := seen.counts(); len(got) != 5 {
		t.Fatalf("%d deliveries, want 5 - the default cap has to apply on its own", len(got))
	}
}

func TestMaxDeliveriesZeroIsUnbounded(t *testing.T) {
	seen := redeliverForever(t, "uncapped", craftnats.WithMaxDeliveries(0))
	seen.waitFor(t, 50, 20*time.Second)
}

// redeliverForever records each delivery of a message always redelivered.
func redeliverForever(t *testing.T, group events.Group, opts ...craftnats.JetStreamOption) *attempts {
	t.Helper()
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, append(opts, craftnats.WithAckWait(2*time.Second))...)

	seen := &attempts{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: group,
		Handle: func(_ context.Context, m *events.Message) error {
			seen.add(m.Deliveries())
			m.Redeliver() // never gives up
			return errors.New("nothing can handle this")
		},
	}}); err != nil {
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

func TestASlowHandlerIsNotRedeliveredBehindItself(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn, craftnats.WithAckWait(time.Second))

	var deliveries atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "slow",
		Handle: func(context.Context, *events.Message) error {
			deliveries.Add(1)
			time.Sleep(4 * time.Second) // four times AckWait
			return nil
		},
	}}); err != nil {
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

// JetStream can settle, redeliver and reject, and nothing else.
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

// JetStreamMsgFrom finds a JetStream delivery's message and MsgFrom does not.
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
	if err := tr.Subscribe(ctx, []events.Subscription{{
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
	}}); err != nil {
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

// Every message of a published batch is delivered.
func TestJetStreamPublishBatch(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	var got atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "batch",
		Handle: func(context.Context, *events.Message) error {
			got.Add(1)
			return nil
		},
	}}); err != nil {
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

// The error handler hears when the cap turns a Redeliver into a termination.
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
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "capped-report",
		Handle: func(_ context.Context, m *events.Message) error {
			m.Redeliver() // never gives up
			return nil    // and never fails, so nothing else reports
		},
	}}); err != nil {
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

// storedInOrders returns how many messages the ORDERS stream holds.
func storedInOrders(t *testing.T, conn *natsclient.Conn) uint64 {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := js.Stream(ctx, "ORDERS")
	if err != nil {
		t.Fatalf("stream ORDERS: %v", err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}

// A batch under a cancelled ctx sends nothing and returns a plain error.
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
	if n := storedInOrders(t, conn); n != 0 {
		t.Errorf("stream holds %d messages, want 0 - a refused batch must not reach the wire", n)
	}
}

// Cancelling ctx once the batch is sent does not cut the ack wait short.
func TestACancellationAfterThePublishDoesNotAbandonTheAcks(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The mapping cancels ctx mid-loop, before the first wait begins.
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
	if n := storedInOrders(t, conn); n != 3 {
		t.Errorf("stream holds %d messages, want 3", n)
	}
}

// A batch stopped by a refused message still waits for the ones in flight.
func TestPublishBatchDoesNotClaimAnUnwaitedMessageWasSent(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")

	// The client rejects the empty subject, stopping the batch at index 1
	// while index 0, on a subject no stream carries, is in flight.
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
	if n := storedInOrders(t, conn); n != 0 {
		t.Errorf("stream holds %d messages, want 0", n)
	}
}

// Only the message no stream carries is unsent; its neighbours are stored.
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
	if n := storedInOrders(t, conn); n != 2 {
		t.Errorf("stream holds %d messages, want 2", n)
	}
}

// A missing ack reports its message unsent once the ack timeout passes.
func TestPublishBatchGivesUpOnAnAcknowledgementThatNeverComes(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn)
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

// quietStream provisions QUIET, a stream over orders.> that stores messages but
// acks none.
func quietStream(t *testing.T, conn *natsclient.Conn) {
	t.Helper()
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
}

// Close ends a waiting batch with ErrClosed, reporting its messages unsent.
func TestCloseEndsAPublishThatIsWaiting(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn)
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
	if n := storedInOrders(t, conn); n != 0 {
		t.Errorf("stream holds %d messages, want 0 - a refused batch must not reach the wire", n)
	}
}

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

func TestThePublishAckTimeoutBoundsTheSynchronousPublishToo(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn)
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

// A zero ack timeout keeps a publish and a batch waiting past the client's 5s default, until Close.
func TestAZeroPublishAckTimeoutWaitsUntilCloseOnBothPaths(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn)
	tr := jsTransport(t, conn, craftnats.WithPublishAckTimeout(0))

	waiting := map[string]chan error{"Publish": make(chan error, 1), "PublishBatch": make(chan error, 1)}
	go func() {
		waiting["Publish"] <- tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)})
	}()
	go func() {
		waiting["PublishBatch"] <- tr.PublishBatch(context.Background(), []*events.Message{{Event: "orders.Paid", Payload: []byte(`{}`)}})
	}()

	select {
	case err := <-waiting["Publish"]:
		t.Fatalf("Publish returned %v before Close", err)
	case err := <-waiting["PublishBatch"]:
		t.Fatalf("PublishBatch returned %v before Close", err)
	case <-time.After(6 * time.Second):
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for path, done := range waiting {
		select {
		case err := <-done:
			if !errors.Is(err, craftnats.ErrClosed) {
				t.Errorf("%s: err = %v, want ErrClosed", path, err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("%s: Close did not end the wait", path)
		}
	}
}

// A ctx that ends during the ack wait does not fail a stored message.
func TestPublishDoesNotFailAMessageTheStreamStored(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Microsecond)
	defer cancel()

	if err := tr.Publish(ctx, &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("publish: %v - the stream stored this, so reporting it failed has the caller publish it twice", err)
	}
	if n := storedInOrders(t, conn); n != 1 {
		t.Errorf("stream holds %d messages, want 1", n)
	}
}

func TestPublishRefusesAnAlreadyCancelledContext(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := tr.Publish(ctx, &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := storedInOrders(t, conn); n != 0 {
		t.Errorf("stream holds %d messages, want 0", n)
	}
}

func TestCloseEndsASinglePublishThatIsWaiting(t *testing.T) {
	conn := runJetStreamServer(t)
	quietStream(t, conn)
	tr := jsTransport(t, conn, craftnats.WithPublishAckTimeout(60*time.Second))

	done := make(chan error, 1)
	started := time.Now()
	go func() {
		done <- tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)})
	}()

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
			t.Errorf("err = %v, want ErrClosed", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not end the publish")
	}
}

func TestPublishAfterCloseSendsNothing(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)

	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); !errors.Is(err, craftnats.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := storedInOrders(t, conn); n != 0 {
		t.Errorf("stream holds %d messages, want 0", n)
	}
}

// Subscribe after Close returns ErrClosed and creates no durable.
func TestSubscribeAfterCloseIsRefused(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	tr := jsTransport(t, conn)
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "late",
		Handle: func(context.Context, *events.Message) error { return nil },
	}})
	if !errors.Is(err, craftnats.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Consumer(ctx, "ORDERS", "late"); !errors.Is(err, jetstream.ErrConsumerNotFound) {
		t.Errorf("durable lookup: err = %v, want ErrConsumerNotFound - a refused subscribe created it", err)
	}
}

// runOnLog is a slog handler that runs fn at its first record.
type runOnLog struct {
	once sync.Once
	fn   func()
}

func (h *runOnLog) Enabled(context.Context, slog.Level) bool { return true }

func (h *runOnLog) Handle(context.Context, slog.Record) error {
	h.once.Do(h.fn)
	return nil
}

func (h *runOnLog) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *runOnLog) WithGroup(string) slog.Handler { return h }

// A Close that runs while Subscribe creates the durable leaves nothing consuming.
func TestACloseDuringSubscribeLeavesNothingConsuming(t *testing.T) {
	conn := runJetStreamServer(t)
	provision(t, conn, "ORDERS", "orders.>")
	var tr *craftnats.JetStream
	tr = jsTransport(t, conn, craftnats.WithJetStreamLogger(slog.New(&runOnLog{fn: func() { _ = tr.Close() }})))

	delivered := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "racing",
		Handle: func(context.Context, *events.Message) error {
			delivered <- struct{}{}
			return nil
		},
	}})
	if !errors.Is(err, craftnats.ErrClosed) {
		t.Errorf("err = %v, want ErrClosed", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(ctx, "orders.Placed", []byte(`{}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-delivered:
		t.Error("a consumer started during Close is delivering")
	case <-time.After(time.Second):
	}
}

// deletedDurableGroup runs handle in group "deleted-durable" on one message.
func deletedDurableGroup(t *testing.T, conn *natsclient.Conn, handle func(context.Context, *events.Message) error) (*craftnats.JetStream, <-chan error, jetstream.Consumer) {
	t.Helper()
	provision(t, conn, "ORDERS", "orders.>")

	reported := make(chan error, 8)
	tr := jsTransport(t, conn, craftnats.WithJetStreamErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) {
			select {
			case reported <- err:
			default:
			}
		}))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "deleted-durable", Handle: handle,
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := tr.Publish(context.Background(), &events.Message{
		Event: "orders.Placed", Key: "o-1", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	infoCtx, infoCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer infoCancel()
	durable, err := js.Consumer(infoCtx, "ORDERS", "deleted-durable")
	if err != nil {
		t.Fatalf("consumer handle: %v", err)
	}
	return tr, reported, durable
}

// deleteConsumer deletes durable from under a running process.
func deleteConsumer(t *testing.T, conn *natsclient.Conn, durable jetstream.Consumer) {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info := durable.CachedInfo()
	if err := js.DeleteConsumer(ctx, info.Stream, info.Name); err != nil {
		t.Fatalf("delete consumer %s: %v", info.Name, err)
	}
}

// awaitConsumerStopped waits for the group's ErrConsumerStopped report.
func awaitConsumerStopped(t *testing.T, reported <-chan error, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case err := <-reported:
			if !errors.Is(err, craftnats.ErrConsumerStopped) {
				continue
			}
			for _, want := range []string{"deleted-durable", "ORDERS"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("report %q does not name %q", err, want)
				}
			}
			return
		case <-deadline:
			t.Fatalf("a deleted durable was never reported as ErrConsumerStopped within %s", within)
		}
	}
}

// deleteUnderAWaitingPull deletes the durable once the group has consumed and its next pull
// waits, so the server answers that pull with the deletion at once.
func deleteUnderAWaitingPull(t *testing.T, conn *natsclient.Conn, delivered <-chan struct{}, durable jetstream.Consumer) {
	t.Helper()
	select {
	case <-delivered:
	case <-time.After(20 * time.Second):
		t.Fatal("the group never consumed, so there is nothing to delete underneath it")
	}

	waiting := time.Now().Add(20 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, err := durable.Info(ctx)
		cancel()
		if err != nil {
			t.Fatalf("consumer info: %v", err)
		}
		if info.NumWaiting >= 1 {
			break
		}
		if time.Now().After(waiting) {
			t.Fatal("the client never left a pull request waiting, so a delete would have nothing to answer")
		}
		time.Sleep(50 * time.Millisecond)
	}
	deleteConsumer(t, conn, durable)
}

// signal is a handler that reports each delivery on delivered without blocking.
func signal(delivered chan<- struct{}) events.Handler {
	return func(context.Context, *events.Message) error {
		select {
		case delivered <- struct{}{}:
		default:
		}
		return nil
	}
}

// A durable deleted under a waiting pull is reported as ErrConsumerStopped.
func TestADeletedDurableWithAPullWaitingIsReportedAtOnce(t *testing.T) {
	conn := runJetStreamServer(t)

	delivered := make(chan struct{}, 1)
	_, reported, durable := deletedDurableGroup(t, conn, signal(delivered))
	deleteUnderAWaitingPull(t, conn, delivered, durable)

	awaitConsumerStopped(t, reported, 20*time.Second)
}

// A group is free to subscribe again by the time its ErrConsumerStopped is reported.
func TestAGroupWhoseDurableWasDeletedCanSubscribeAgain(t *testing.T) {
	conn := runJetStreamServer(t)

	delivered := make(chan struct{}, 1)
	tr, reported, durable := deletedDurableGroup(t, conn, signal(delivered))
	deleteUnderAWaitingPull(t, conn, delivered, durable)
	awaitConsumerStopped(t, reported, 20*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "deleted-durable", Handle: signal(delivered),
	}}); err != nil {
		t.Fatalf("subscribe after ErrConsumerStopped: %v", err)
	}
}

func TestADeletedDurableWithNoPullWaitingIsReportedOnTheNextMissedHeartbeat(t *testing.T) {
	conn := runJetStreamServer(t)

	delivered := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseHandler)

	_, reported, durable := deletedDurableGroup(t, conn, func(context.Context, *events.Message) error {
		select {
		case delivered <- struct{}{}:
		default:
		}
		<-release
		return nil
	})

	select {
	case <-delivered:
	case <-time.After(20 * time.Second):
		t.Fatal("the group never consumed, so there is nothing to delete underneath it")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := durable.Info(ctx)
	if err != nil {
		t.Fatalf("consumer info: %v", err)
	}
	if info.NumWaiting != 0 {
		t.Fatalf("NumWaiting = %d while the handler runs, so the delete would be answered after all", info.NumWaiting)
	}
	deleteConsumer(t, conn, durable)
	if _, err := durable.Info(ctx); !errors.Is(err, jetstream.ErrConsumerNotFound) {
		t.Fatalf("info after the delete: err = %v, want ErrConsumerNotFound", err)
	}
	releaseHandler()

	awaitConsumerStopped(t, reported, 60*time.Second)
}
