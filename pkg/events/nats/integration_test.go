package nats_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsclient "github.com/nats-io/nats.go"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	craftnats "github.com/craftgodotdev/craftgo/pkg/events/nats"
)

// runServer starts an in-process NATS server, so the adapter is exercised
// against a real broker without Docker or a network.
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

// A published contract reaches a consumer with its key and payload intact.
func TestRoundTripOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) { t.Errorf("handler failed: %v", err) }))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *events.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.OrderPlaced", Consumer: "SendReceipt",
		Handle: func(_ context.Context, m *events.Message) error { got <- m; return nil },
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
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

// Two consumer names are two groups: each gets its own copy. Two
// subscriptions sharing a name are one group and split the work.
func TestConsumerGroupsOverNATS(t *testing.T) {
	conn := runServer(t)
	bus := events.New(events.WithTransport(craftnats.New(conn)), events.WithCodec(codecjson.Codec{}))
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
	for _, group := range []string{"SendReceipt", "MirrorStock"} {
		if err := bus.Subscribe(ctx, events.Subscription{
			Event: "orders.OrderPlaced", Consumer: group, Handle: add(group),
		}); err != nil {
			t.Fatalf("subscribe %s: %v", group, err)
		}
	}
	// A second replica of one group shares its work rather than duplicating.
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.OrderPlaced", Consumer: "SendReceipt", Handle: add("SendReceipt"),
	}); err != nil {
		t.Fatal(err)
	}
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

// A batch reaches the broker in one call and every message arrives.
func TestBatchOverNATS(t *testing.T) {
	conn := runServer(t)
	bus := events.New(events.WithTransport(craftnats.New(conn)), events.WithCodec(codecjson.Codec{}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 4)
	for _, contract := range []string{"orders.OrderPlaced", "inventory.Reserved"} {
		if err := bus.Subscribe(ctx, events.Subscription{
			Event: contract, Consumer: "Probe",
			Handle: func(_ context.Context, m *events.Message) error { seen <- m.Event; return nil },
		}); err != nil {
			t.Fatal(err)
		}
	}
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

// A panicking handler must not end the process on a broker either. The
// recover the Bus installs runs on the goroutine NATS delivers on, so the
// adapter inherits it without knowing about it.
func TestHandlerPanicOverNATS(t *testing.T) {
	conn := runServer(t)
	failures := make(chan error, 2)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) { failures <- err }))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	delivered := make(chan string, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
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
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
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

// A caller's metadata reaches the consumer over a real broker, and the
// codec stamp travels with it.
func TestCallerMetadataOverNATS(t *testing.T) {
	conn := runServer(t)
	tr := craftnats.New(conn, craftnats.WithErrorHandler(
		func(_ events.Subscription, _ *events.Message, err error) { t.Errorf("handler failed: %v", err) }))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *events.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Subscribe(ctx, events.Subscription{
		Event: "orders.OrderPlaced", Consumer: "SendReceipt",
		Handle: func(_ context.Context, m *events.Message) error { got <- m; return nil },
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
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

// A middleware reaching for what events.Message does not carry gets the
// NATS message the delivery came from - proved through the real Subscribe
// path against a real server, because the wiring is what is under test
// and a unit test of the accessor would not exercise it.
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
	if err := tr.Subscribe(ctx, events.Subscription{
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
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Published raw so the message carries a header this adapter does not
	// map, which is the reason to reach for the raw message at all.
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
