package nats_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsclient "github.com/nats-io/nats.go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// runServer starts an in-process NATS server and connects to it.
func runServer(t *testing.T) *natsclient.Conn {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
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

type payload struct {
	OrderID string `json:"orderId"`
}

// start registers subs on bus and starts it.
func start(t *testing.T, ctx context.Context, bus *events.Bus, subs ...events.Subscription) {
	t.Helper()
	for _, sub := range subs {
		if err := bus.Register(sub); err != nil {
			t.Fatalf("register %s/%s: %v", sub.Event, sub.Consumer, err)
		}
	}
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
}

// A published contract reaches a consumer with its key and payload intact.
func TestRoundTripOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) { t.Errorf("handler failed: %v", err) }))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *events.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start(t, ctx, bus, events.Subscription{
		Event: "orders.OrderPlaced", Consumer: "SendReceipt", Group: "receipts",
		Handle: func(_ context.Context, m *events.Message) error { got <- m; return nil },
	})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(ctx, "orders.OrderPlaced", payload{OrderID: "order-1"}, events.WithKey("order-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case m := <-got:
		if m.Event != "orders.OrderPlaced" {
			t.Errorf("contract = %q", m.Event)
		}
		if m.Key != "order-1" {
			t.Errorf("key = %q, want order-1", m.Key)
		}
		var p payload
		if err := bus.Decode(m, &p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if p.OrderID != "order-1" {
			t.Errorf("payload = %+v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no delivery")
	}
}

// Each group receives every message; replicas of one group share them.
func TestConsumerGroupsOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn)
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	hits := map[string]int{}
	add := func(name string) func(context.Context, *events.Message) error {
		return func(context.Context, *events.Message) error {
			mu.Lock()
			hits[name]++
			mu.Unlock()
			return nil
		}
	}
	for _, group := range []events.Group{"SendReceipt", "MirrorStock"} {
		start(t, ctx, events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{})),
			events.Subscription{
				Event: "orders.OrderPlaced", Consumer: string(group), Group: group, Handle: add(string(group)),
			})
	}
	// A second replica of one group: a second bus over the same connection.
	start(t, ctx, events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{})),
		events.Subscription{
			Event: "orders.OrderPlaced", Consumer: "SendReceipt", Group: "SendReceipt", Handle: add("SendReceipt"),
		})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}

	const n = 6
	for i := 0; i < n; i++ {
		if err := bus.Publish(ctx, "orders.OrderPlaced", payload{OrderID: "x"}, events.WithKey("k")); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		done := hits["SendReceipt"] == n && hits["MirrorStock"] == n
		mu.Unlock()
		if done {
			return
		}
		select {
		case <-deadline:
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("each group should receive all %d; got %v", n, hits)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Cancelling a subscription's context unsubscribes it.
func TestCancellingTheContextStopsDeliveryOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn)
	var delivered atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error {
			delivered.Add(1)
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for conn.NumSubscriptions() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the subscription outlived its context")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tr.Publish(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := delivered.Load(); n != 0 {
		t.Errorf("%d deliveries after the context ended, want 0", n)
	}
}

// A subscription on a context that never ends parks no goroutine waiting for it.
func TestASubscriptionOnAContextThatNeverEndsParksNoWatcher(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn)
	defer func() { _ = tr.Close() }()
	if err := tr.Subscribe(context.Background(), []events.Subscription{{
		Event: "orders.Placed", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	for deadline := time.Now().Add(200 * time.Millisecond); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if n := parkedOnANilChannel(); n > 0 {
			t.Fatalf("%d goroutine(s) wait for a context that never ends", n)
		}
	}
}

// parkedOnANilChannel counts this adapter's goroutines blocked for ever on a nil channel.
func parkedOnANilChannel() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "[chan receive (nil chan)") && strings.Contains(g, "pkg/events/nats.") {
			n++
		}
	}
	return n
}

// A batch reaches the broker in one call and every message arrives.
func TestBatchOverNATS(t *testing.T) {
	conn := runServer(t)
	bus := events.New(events.WithTransport(craftnats.New(conn)), events.WithCodec(codecjson.Codec{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 4)
	var subs []events.Subscription
	for _, contract := range []string{"orders.OrderPlaced", "inventory.Reserved"} {
		subs = append(subs, events.Subscription{
			Event: contract, Consumer: "Probe", Group: "probe",
			Handle: func(_ context.Context, m *events.Message) error { seen <- m.Event; return nil },
		})
	}
	start(t, ctx, bus, subs...)
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}

	if err := bus.PublishAll(ctx, []events.Envelope{
		{Event: "orders.OrderPlaced", Key: "o1", Payload: payload{OrderID: "o1"}},
		{Event: "inventory.Reserved", Key: "s1", Payload: payload{OrderID: "s1"}},
	}); err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case c := <-seen:
			got[c] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("only got %v", got)
		}
	}
	if !got["orders.OrderPlaced"] || !got["inventory.Reserved"] {
		t.Errorf("batch lost a contract: %v", got)
	}
}

// A panicking handler is reported as a PanicError and delivery continues.
func TestHandlerPanicOverNATS(t *testing.T) {
	conn := runServer(t)
	failures := make(chan error, 2)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) { failures <- err }))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	delivered := make(chan string, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start(t, ctx, bus, events.Subscription{
		Event: "orders.OrderPlaced", Consumer: "SendReceipt", Group: "receipts",
		Handle: func(_ context.Context, m *events.Message) error {
			var p payload
			if err := bus.Decode(m, &p); err != nil {
				return err
			}
			if p.OrderID == "boom" {
				panic("handler exploded")
			}
			delivered <- p.OrderID
			return nil
		},
	})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"boom", "order-2"} {
		if err := bus.Publish(ctx, "orders.OrderPlaced", payload{OrderID: id}, events.WithKey(id)); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}

	select {
	case err := <-failures:
		var pe *events.PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("recovered panic is not a *PanicError: %#v", err)
		}
		if pe.Consumer != "SendReceipt" || pe.Group != "receipts" {
			t.Errorf("panic error does not name the subscription: %+v", pe)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the panic never reached the error handler")
	}

	select {
	case id := <-delivered:
		if id != "order-2" {
			t.Errorf("delivered %q, want order-2", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delivery stopped after the panic")
	}
}

// Caller metadata and the codec stamp reach the consumer.
func TestCallerMetadataOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) { t.Errorf("handler failed: %v", err) }))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *events.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start(t, ctx, bus, events.Subscription{
		Event: "orders.OrderPlaced", Consumer: "SendReceipt", Group: "receipts",
		Handle: func(_ context.Context, m *events.Message) error { got <- m; return nil },
	})
	if err := conn.Flush(); err != nil {
		t.Fatal(err)
	}

	if err := bus.PublishAll(ctx, []events.Envelope{{
		Event:    "orders.OrderPlaced",
		Key:      "order-1",
		Payload:  payload{OrderID: "order-1"},
		Metadata: map[string]string{"hops": "2", "dead-letter": "true"},
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case m := <-got:
		if m.Key != "order-1" {
			t.Errorf("key = %q, want order-1", m.Key)
		}
		if m.Metadata["hops"] != "2" || m.Metadata["dead-letter"] != "true" {
			t.Errorf("caller metadata = %v", m.Metadata)
		}
		if m.Metadata[events.MetaCodec] != "json" {
			t.Errorf("codec stamp lost: %v", m.Metadata)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no delivery")
	}
}

// MsgFrom returns the delivered message on the real Subscribe path.
func TestTheRawMessageIsReachableOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn)
	defer func() { _ = tr.Close() }()

	type seen struct {
		subject  string
		reply    string
		unmapped string
		found    bool
	}
	var (
		mu   sync.Mutex
		got  seen
		done = make(chan struct{})
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Subscribe(ctx, []events.Subscription{{
		Event: "orders.OrderPlaced", Consumer: "C", Group: "raw",
		Handle: func(hctx context.Context, _ *events.Message) error {
			mu.Lock()
			if m, ok := craftnats.MsgFrom(hctx); ok {
				got = seen{
					subject:  m.Subject,
					reply:    m.Reply,
					unmapped: m.Header.Get("x-unmapped"),
					found:    true,
				}
			}
			mu.Unlock()
			close(done)
			return nil
		},
	}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Published raw so the message carries a header this adapter does not map.
	raw := natsclient.NewMsg("orders.OrderPlaced")
	raw.Data = []byte(`{"orderId":"o-1"}`)
	raw.Header.Set("x-unmapped", "kept")
	if err := conn.PublishMsg(raw); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
	}
	mu.Lock()
	defer mu.Unlock()
	if !got.found {
		t.Fatal("MsgFrom found no message on a NATS delivery - Subscribe did not carry it")
	}
	if got.subject != "orders.OrderPlaced" {
		t.Errorf("subject = %q", got.subject)
	}
	if got.unmapped != "kept" {
		t.Errorf("the unmapped header is not reachable: %q", got.unmapped)
	}
}
