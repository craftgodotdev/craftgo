package events_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

type payload struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// recordingTransport records what it is handed and delivers nothing.
type recordingTransport struct {
	mu      sync.Mutex
	sent    []*events.Message
	subs    []events.Subscription
	batches int
	err     error
}

func (r *recordingTransport) Publish(_ context.Context, msg *events.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return nil
}

func (r *recordingTransport) Subscribe(_ context.Context, subs []events.Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches++
	r.subs = append(r.subs, subs...)
	return r.err
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

func TestBusPublishEncodesWithCodec(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1", Count: 2}, events.WithKey("o-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(tr.sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(tr.sent))
	}
	msg := tr.sent[0]
	if msg.Event != "orders.OrderPlaced" || msg.Key != "o-1" {
		t.Fatalf("envelope mismatch: %+v", msg)
	}
	if got := msg.Metadata[events.MetaCodec]; got != "json" {
		t.Fatalf("codec metadata = %q, want json", got)
	}
	if !strings.Contains(string(msg.Payload), `"id":"o-1"`) {
		t.Fatalf("payload = %s", msg.Payload)
	}
}

func TestBusWithoutCodecRefusesToGuess(t *testing.T) {
	bus := events.New(events.WithTransport(&recordingTransport{}))
	err := bus.Publish(context.Background(), "x.Y", payload{})
	if !errors.Is(err, events.ErrNoCodec) {
		t.Fatalf("want ErrNoCodec, got %v", err)
	}
}

func TestBusPublishWithoutPublisher(t *testing.T) {
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	if err := bus.Publish(context.Background(), "x.Y", payload{}); !errors.Is(err, events.ErrNoPublisher) {
		t.Fatalf("want ErrNoPublisher, got %v", err)
	}
	if err := bus.Register(events.Subscription{
		Event: "x.Y", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); !errors.Is(err, events.ErrNoSubscriber) {
		t.Fatalf("want ErrNoSubscriber, got %v", err)
	}
}

// mismatchCodec wraps a codec under the name "protobuf".
type mismatchCodec struct{ events.Codec }

func (mismatchCodec) Name() string { return "protobuf" }

func TestBusDecodeRejectsForeignCodec(t *testing.T) {
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	msg := &events.Message{
		Event:    "orders.OrderPlaced",
		Payload:  []byte(`{"id":"o-1"}`),
		Metadata: map[string]string{events.MetaCodec: "protobuf"},
	}
	var p payload
	err := bus.Decode(msg, &p)
	if err == nil || !strings.Contains(err.Error(), "protobuf") {
		t.Fatalf("want codec-mismatch error, got %v", err)
	}
}

func TestBusPerEventCodec(t *testing.T) {
	bus := events.New(
		events.WithCodec(codecjson.Codec{}),
		events.WithCodecFor("orders.OrderPlaced", mismatchCodec{Codec: codecjson.Codec{}}),
	)
	c, err := bus.CodecFor("orders.OrderPlaced")
	if err != nil || c.Name() != "protobuf" {
		t.Fatalf("per-event codec = %v, %v", c, err)
	}
	c, err = bus.CodecFor("orders.Other")
	if err != nil || c.Name() != "json" {
		t.Fatalf("default codec = %v, %v", c, err)
	}
}

func TestMemoryTransportRoundTrip(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	var mu sync.Mutex
	var got []payload
	start(t, context.Background(), bus, events.Subscription{
		Event:    "orders.OrderPlaced",
		Consumer: "SendReceipt",
		Group:    "receipts",
		Handle: func(_ context.Context, msg *events.Message) error {
			var p payload
			if err := bus.Decode(msg, &p); err != nil {
				return err
			}
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
			return nil
		},
	})
	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1", Count: 7}, events.WithKey("o-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].ID != "o-1" || got[0].Count != 7 {
		t.Fatalf("delivered = %+v", got)
	}
}

// Two replicas of one group, each on its own bus, divide the messages between them, and
// a second group gets its own copy.
func TestMemoryTransportCompetingConsumers(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	var mu sync.Mutex
	hits := map[int]int{}
	for i := 0; i < 2; i++ {
		// A per-iteration copy: this module's go version shares loop variables.
		i := i
		replica := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
		start(t, context.Background(), replica, events.Subscription{
			Event:    "orders.OrderPlaced",
			Consumer: "SendReceipt",
			Group:    "receipts",
			Handle: func(context.Context, *events.Message) error {
				mu.Lock()
				hits[i]++
				mu.Unlock()
				return nil
			},
		})
	}
	var otherGroup int
	start(t, context.Background(), bus, events.Subscription{
		Event:    "orders.OrderPlaced",
		Consumer: "UpdateSearchIndex",
		Group:    "search",
		Handle: func(context.Context, *events.Message) error {
			mu.Lock()
			otherGroup++
			mu.Unlock()
			return nil
		},
	})

	for i := 0; i < 4; i++ {
		if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if total := hits[0] + hits[1]; total != 4 {
		t.Fatalf("group receipts handled %d of 4 messages (%v)", total, hits)
	}
	if otherGroup != 4 {
		t.Fatalf("group search handled %d of 4 messages", otherGroup)
	}
}

func TestMemoryTransportErrorHandler(t *testing.T) {
	var mu sync.Mutex
	var seen error
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		mu.Lock()
		seen = err
		mu.Unlock()
	}))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	boom := errors.New("boom")
	start(t, context.Background(), bus, events.Subscription{
		Event:    "x.Y",
		Consumer: "C",
		Group:    "g",
		Handle:   func(context.Context, *events.Message) error { return boom },
	})
	if err := bus.Publish(context.Background(), "x.Y", payload{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(seen, boom) {
		t.Fatalf("error handler saw %v", seen)
	}
}

func TestMemoryTransportStopsOnContextCancel(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	delivered := 0
	start(t, ctx, bus, events.Subscription{
		Event:    "x.Y",
		Consumer: "C",
		Group:    "g",
		Handle: func(context.Context, *events.Message) error {
			mu.Lock()
			delivered++
			mu.Unlock()
			return nil
		},
	})
	if err := bus.Publish(ctx, "x.Y", payload{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()
	cancel()
	for i := 0; i < 100; i++ {
		if err := bus.Publish(context.Background(), "x.Y", payload{}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		tr.Drain()
		mu.Lock()
		n := delivered
		mu.Unlock()
		if n == 1 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if delivered < 1 {
		t.Fatalf("expected the pre-cancel delivery, got %d", delivered)
	}
}

// batchRecorder is a transport that implements the optional batch upgrade.
type batchRecorder struct {
	batches [][]*events.Message
	single  int
}

func (r *batchRecorder) Publish(_ context.Context, msg *events.Message) error {
	r.single++
	return nil
}

func (r *batchRecorder) PublishBatch(_ context.Context, msgs []*events.Message) error {
	r.batches = append(r.batches, msgs)
	return nil
}

// A transport that can take a batch gets one call carrying every message, each encoded
// with its own contract's codec.
func TestPublishAllUsesTheBatchUpgrade(t *testing.T) {
	rec := &batchRecorder{}
	bus := events.New(events.WithPublisher(rec), events.WithCodec(codecjson.Codec{}))
	err := bus.PublishAll(context.Background(), []events.Envelope{
		{Event: "orders.OrderPlaced", Key: "o-1", Payload: map[string]string{"id": "o-1"}},
		{Event: "inventory.Reserved", Key: "sku-9", Payload: map[string]string{"sku": "sku-9"}},
	})
	if err != nil {
		t.Fatalf("publish all: %v", err)
	}
	if len(rec.batches) != 1 {
		t.Fatalf("expected one batch call, got %d", len(rec.batches))
	}
	if rec.single != 0 {
		t.Errorf("the single-message path ran %d time(s)", rec.single)
	}
	got := rec.batches[0]
	if len(got) != 2 || got[0].Event != "orders.OrderPlaced" || got[1].Event != "inventory.Reserved" {
		t.Errorf("batch lost order or contracts: %+v", got)
	}
	if got[0].Key != "o-1" || got[1].Key != "sku-9" {
		t.Errorf("batch lost keys: %+v", got)
	}
}

// plainRecorder implements only Publisher.
type plainRecorder struct{ msgs []*events.Message }

func (r *plainRecorder) Publish(_ context.Context, msg *events.Message) error {
	r.msgs = append(r.msgs, msg)
	return nil
}

// A transport without the batch upgrade is sent one message at a time, in order.
func TestPublishAllFallsBackToOneAtATime(t *testing.T) {
	rec := &plainRecorder{}
	bus := events.New(events.WithPublisher(rec), events.WithCodec(codecjson.Codec{}))
	if err := bus.PublishAll(context.Background(), []events.Envelope{
		{Event: "a.One", Payload: map[string]int{"n": 1}},
		{Event: "b.Two", Payload: map[string]int{"n": 2}},
	}); err != nil {
		t.Fatalf("publish all: %v", err)
	}
	if len(rec.msgs) != 2 || rec.msgs[0].Event != "a.One" || rec.msgs[1].Event != "b.Two" {
		t.Errorf("expected two messages in order, got %+v", rec.msgs)
	}
}

// An unencodable payload fails the batch before anything is sent.
func TestPublishAllEncodesBeforeSending(t *testing.T) {
	rec := &plainRecorder{}
	bus := events.New(events.WithPublisher(rec), events.WithCodec(codecjson.Codec{}))
	err := bus.PublishAll(context.Background(), []events.Envelope{
		{Event: "a.One", Payload: map[string]int{"n": 1}},
		{Event: "b.Two", Payload: make(chan int)},
	})
	if err == nil {
		t.Fatal("expected an encode error")
	}
	if len(rec.msgs) != 0 {
		t.Errorf("a message was sent before the batch finished encoding: %+v", rec.msgs)
	}
}

// failAfter publishes the first n messages and then errors.
type failAfter struct {
	n    int
	sent []string
}

func (f *failAfter) Publish(_ context.Context, msg *events.Message) error {
	if len(f.sent) >= f.n {
		return errors.New("broker rejected")
	}
	f.sent = append(f.sent, msg.Event)
	return nil
}

// A one-at-a-time batch that stops partway reports the unsent tail.
func TestPublishAllReportsProgressOnPartialFailure(t *testing.T) {
	rec := &failAfter{n: 2}
	bus := events.New(events.WithPublisher(rec), events.WithCodec(codecjson.Codec{}))
	envs := []events.Envelope{
		{Event: "a.One", Payload: 1},
		{Event: "b.Two", Payload: 2},
		{Event: "c.Three", Payload: 3},
		{Event: "d.Four", Payload: 4},
	}
	err := bus.PublishAll(context.Background(), envs)
	if err == nil {
		t.Fatal("expected a failure")
	}
	var partial *events.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("expected a *PartialPublishError, got %T: %v", err, err)
	}
	if partial.Sent != 2 {
		t.Errorf("Sent = %d, want 2", partial.Sent)
	}
	if partial.Event != "c.Three" {
		t.Errorf("failed on %q, want c.Three", partial.Event)
	}
	if !strings.Contains(err.Error(), "2 already sent") {
		t.Errorf("error does not say how far it got: %v", err)
	}
	if got := partial.Unsent; len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("Unsent = %v, want [2 3]", got)
	}
}

// Two differently named consumers sharing one group split the stream.
func TestMemoryTransportGroupsOnTheGroupName(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	var mu sync.Mutex
	hits := map[string]int{}
	for _, consumer := range []string{"SendReceipt", "RecordReceipt"} {
		consumer := consumer
		// One bus takes one registration per contract and group.
		replica := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
		start(t, context.Background(), replica, events.Subscription{
			Event:    "orders.OrderPlaced",
			Consumer: consumer,
			Group:    "order-worker",
			Handle: func(context.Context, *events.Message) error {
				mu.Lock()
				hits[consumer]++
				mu.Unlock()
				return nil
			},
		})
	}
	for i := 0; i < 4; i++ {
		if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if total := hits["SendReceipt"] + hits["RecordReceipt"]; total != 4 {
		t.Fatalf("one group handled %d of 4 messages (%v)", total, hits)
	}
}

// deliverOne runs one message through handle on the in-process transport and returns
// what the transport's error handler saw.
func deliverOne(t *testing.T, handle func(context.Context, *events.Message) error) error {
	t.Helper()
	var mu sync.Mutex
	var seen error
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		mu.Lock()
		seen = err
		mu.Unlock()
	}))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	start(t, context.Background(), bus, events.Subscription{
		Event: "x.Y", Consumer: "C", Group: "g", Handle: handle,
	})
	if err := bus.Publish(context.Background(), "x.Y", payload{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()
	mu.Lock()
	defer mu.Unlock()
	return seen
}

// A panicking handler reaches the transport's error handler as a *PanicError, and
// delivery continues.
func TestPanicInAHandlerReachesTheErrorHandler(t *testing.T) {
	var mu sync.Mutex
	var seen []error
	var handled []string
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		mu.Lock()
		seen = append(seen, err)
		mu.Unlock()
	}))
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	start(t, context.Background(), bus, events.Subscription{
		Event:    "orders.OrderPlaced",
		Consumer: "SendReceipt",
		Group:    "receipts",
		Handle: func(_ context.Context, msg *events.Message) error {
			var p payload
			if err := bus.Decode(msg, &p); err != nil {
				return err
			}
			if p.ID == "boom" {
				panic("handler exploded")
			}
			mu.Lock()
			handled = append(handled, p.ID)
			mu.Unlock()
			return nil
		},
	})
	for _, id := range []string{"boom", "o-2"} {
		if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: id}, events.WithKey(id)); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("error handler saw %d errors, want 1: %v", len(seen), seen)
	}
	var pe *events.PanicError
	if !errors.As(seen[0], &pe) {
		t.Fatalf("recovered panic is not a *PanicError: %#v", seen[0])
	}
	if pe.Consumer != "SendReceipt" || pe.Group != "receipts" || pe.Event != "orders.OrderPlaced" {
		t.Errorf("panic error does not name the subscription: %+v", pe)
	}
	if pe.Value != "handler exploded" {
		t.Errorf("panic value = %v", pe.Value)
	}
	if len(pe.Stack) == 0 {
		t.Error("panic error carries no stack")
	}
	for _, want := range []string{"SendReceipt", "receipts", "handler exploded"} {
		if !strings.Contains(pe.Error(), want) {
			t.Errorf("message %q does not name %q", pe.Error(), want)
		}
	}
	// The message after the panic is still delivered.
	if len(handled) != 1 || handled[0] != "o-2" {
		t.Errorf("delivery did not continue past the panic: %v", handled)
	}
}

// A recovered panic comes back as a [*PanicError]; an ordinary handler
// error is handed on untouched.
func TestAPanicBecomesAPanicErrorAndAnOrdinaryErrorDoesNot(t *testing.T) {
	panicked := deliverOne(t, func(context.Context, *events.Message) error { panic("boom") })
	var pe *events.PanicError
	if !errors.As(panicked, &pe) {
		t.Errorf("recovered panic is not a *PanicError: %v", panicked)
	}

	boom := errors.New("broker unreachable")
	returned := deliverOne(t, func(context.Context, *events.Message) error { return boom })
	if errors.As(returned, &pe) {
		t.Errorf("an ordinary handler error must not become a *PanicError: %v", returned)
	}
	if !errors.Is(returned, boom) {
		t.Errorf("handler error did not survive the wrapper: %v", returned)
	}
}

// Panicking with an error keeps it reachable through the PanicError;
// panicking with anything else unwraps to nothing.
func TestAPanicErrorUnwrapsToThePanicValueWhenItIsAnError(t *testing.T) {
	boom := errors.New("handler exploded")
	withErr := deliverOne(t, func(context.Context, *events.Message) error { panic(boom) })
	if !errors.Is(withErr, boom) {
		t.Errorf("panicking with an error did not reach it: %v", withErr)
	}

	withString := deliverOne(t, func(context.Context, *events.Message) error { panic("boom") })
	if errors.Unwrap(withString) != nil {
		t.Errorf("panicking with a string unwrapped to %v, want nil", errors.Unwrap(withString))
	}
}

// The transport is handed a handler already wrapped in a recover.
func TestTheTransportIsHandedAGuardedHandler(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	start(t, context.Background(), bus, events.Subscription{
		Event: "x.Y", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { panic("handler exploded") },
	})
	err := tr.subs[0].Handle(context.Background(), &events.Message{Event: "x.Y"})
	var pe *events.PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("the transport was handed an unguarded handler: %#v", err)
	}
	if pe.Group != "g" {
		t.Errorf("panic error group = %q, want g", pe.Group)
	}
}

// A caller's metadata reaches the consumer untouched.
func TestCallerMetadataReachesTheConsumer(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	var mu sync.Mutex
	var got *events.Message
	start(t, context.Background(), bus, events.Subscription{
		Event:    "orders.OrderPlaced",
		Consumer: "SendReceipt",
		Group:    "receipts",
		Handle: func(_ context.Context, msg *events.Message) error {
			mu.Lock()
			got = msg
			mu.Unlock()
			return nil
		},
	})

	if err := bus.PublishAll(context.Background(), []events.Envelope{{
		Event:    "orders.OrderPlaced",
		Key:      "o-1",
		Payload:  payload{ID: "o-1"},
		Metadata: map[string]string{"hops": "1", "dead-letter": "true"},
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("nothing delivered")
	}
	if got.Metadata["hops"] != "1" || got.Metadata["dead-letter"] != "true" {
		t.Fatalf("caller metadata = %v", got.Metadata)
	}
	if got.Metadata[events.MetaCodec] != "json" {
		t.Fatalf("codec stamp lost: %v", got.Metadata)
	}
}

// The codec stamp is the runtime's, so a caller cannot forge it.
func TestTheCodecStampWinsACollision(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.PublishAll(context.Background(), []events.Envelope{{
		Event:    "orders.OrderPlaced",
		Payload:  payload{ID: "o-1"},
		Metadata: map[string]string{events.MetaCodec: "protobuf", "Content-Codec": "protobuf"},
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(tr.sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(tr.sent))
	}
	if got := tr.sent[0].Metadata[events.MetaCodec]; got != "json" {
		t.Fatalf("codec stamp = %q, want the codec that ran", got)
	}
	if _, forged := tr.sent[0].Metadata["Content-Codec"]; forged {
		t.Fatalf("a differently-cased stamp survived: %v", tr.sent[0].Metadata)
	}
}

// A caller's metadata under a reserved key never reaches the transport.
func TestReservedMetadataIsDroppedBeforeTheTransport(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.PublishAll(context.Background(), []events.Envelope{{
		Event:   "orders.OrderPlaced",
		Key:     "o-1",
		Payload: payload{ID: "o-1"},
		Metadata: map[string]string{
			"craftgo-event": "orders.SomethingElse",
			"craftgo-key":   "o-2",
			"Craftgo-Key":   "o-3",
			"hops":          "1",
		},
	}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msg := tr.sent[0]
	if msg.Event != "orders.OrderPlaced" || msg.Key != "o-1" {
		t.Fatalf("envelope was renamed: %+v", msg)
	}
	want := map[string]string{"hops": "1", events.MetaCodec: "json"}
	if !reflect.DeepEqual(msg.Metadata, want) {
		t.Fatalf("metadata = %v, want %v", msg.Metadata, want)
	}
}

// IsReservedMeta accepts the codec key and the adapter prefix, ignoring case, and
// nothing else.
func TestIsReservedMetaNamesTheRuntimeAndAdapterKeys(t *testing.T) {
	reserved := []string{
		events.MetaCodec, "Content-Codec", "CONTENT-CODEC",
		"craftgo-event", "craftgo-key", "Craftgo-Key", "CRAFTGO-anything",
	}
	for _, k := range reserved {
		if !events.IsReservedMeta(k) {
			t.Errorf("IsReservedMeta(%q) = false, want true", k)
		}
	}
	open := []string{"", "hops", "dead-letter", "traceparent", "codec", "craftgo", "x-craftgo-key"}
	for _, k := range open {
		if events.IsReservedMeta(k) {
			t.Errorf("IsReservedMeta(%q) = true, want false", k)
		}
	}
}

// Nil metadata, empty metadata and Bus.Publish all produce the same message.
func TestAnEnvelopeWithoutMetadataEncodesAsBefore(t *testing.T) {
	body, err := codecjson.Codec{}.Marshal(payload{ID: "o-1", Count: 2})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := &events.Message{
		Event:    "orders.OrderPlaced",
		Key:      "o-1",
		Payload:  body,
		Metadata: map[string]string{events.MetaCodec: "json"},
	}

	for _, meta := range []map[string]string{nil, {}} {
		tr := &recordingTransport{}
		bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
		if err := bus.PublishAll(context.Background(), []events.Envelope{{
			Event: "orders.OrderPlaced", Key: "o-1", Payload: payload{ID: "o-1", Count: 2}, Metadata: meta,
		}}); err != nil {
			t.Fatalf("publish with metadata %v: %v", meta, err)
		}
		if !reflect.DeepEqual(tr.sent[0], want) {
			t.Fatalf("metadata %v produced %+v, want %+v", meta, tr.sent[0], want)
		}
	}

	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1", Count: 2}, events.WithKey("o-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !reflect.DeepEqual(tr.sent[0], want) {
		t.Fatalf("Publish produced %+v, want %+v", tr.sent[0], want)
	}
}

// Metadata on one envelope is not metadata on the next.
func TestMetadataIsPerEnvelope(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.PublishAll(context.Background(), []events.Envelope{
		{Event: "orders.OrderPlaced", Payload: payload{ID: "a"}, Metadata: map[string]string{"hops": "1"}},
		{Event: "orders.OrderPlaced", Payload: payload{ID: "b"}},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if tr.sent[0].Metadata["hops"] != "1" {
		t.Fatalf("first envelope lost its metadata: %v", tr.sent[0].Metadata)
	}
	if _, leaked := tr.sent[1].Metadata["hops"]; leaked {
		t.Fatalf("metadata leaked onto the next envelope: %v", tr.sent[1].Metadata)
	}
}

// batchTransport is a BatchPublisher whose report the test dictates.
type batchTransport struct {
	recordingTransport
	report func(msgs []*events.Message) error
}

func (b *batchTransport) PublishBatch(_ context.Context, msgs []*events.Message) error {
	return b.report(msgs)
}

func batchBus(t *batchTransport) *events.Bus {
	return events.New(events.WithTransport(t), events.WithCodec(codecjson.Codec{}))
}

func fourEnvelopes() []events.Envelope {
	return []events.Envelope{
		{Event: "a.One", Payload: 1},
		{Event: "b.Two", Payload: 2},
		{Event: "a.Three", Payload: 3},
		{Event: "b.Four", Payload: 4},
	}
}

// A partial report with gaps passes through naming exactly the unsent indices.
func TestAPartialBatchNamesExactlyTheUnsentIndices(t *testing.T) {
	tr := &batchTransport{report: func(msgs []*events.Message) error {
		return &events.PartialPublishError{
			Sent: 1, Unsent: []int{1, 3}, Event: msgs[1].Event,
			Err: errors.New("topic b unavailable"),
		}
	}}
	err := batchBus(tr).PublishAll(context.Background(), fourEnvelopes())

	var partial *events.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %T %v, want *PartialPublishError", err, err)
	}
	if len(partial.Unsent) != 2 || partial.Unsent[0] != 1 || partial.Unsent[1] != 3 {
		t.Fatalf("Unsent = %v, want [1 3] - the sent ones are not a prefix", partial.Unsent)
	}
	if partial.Sent != partial.Unsent[0] {
		t.Errorf("Sent = %d, want %d (Unsent[0])", partial.Sent, partial.Unsent[0])
	}
	if partial.Event != "b.Two" {
		t.Errorf("Event = %q, want the first unsent envelope's contract", partial.Event)
	}
	envs := fourEnvelopes()
	var retry []string
	for _, i := range partial.Unsent {
		retry = append(retry, envs[i].Event)
	}
	if len(retry) != 2 || retry[0] != "b.Two" || retry[1] != "b.Four" {
		t.Errorf("retrying Unsent resends %v, want the two b envelopes", retry)
	}
}

// An adapter's overstated Sent is corrected to Unsent[0].
func TestAnOverstatedSentIsCorrectedFromUnsent(t *testing.T) {
	tr := &batchTransport{report: func(msgs []*events.Message) error {
		return &events.PartialPublishError{
			Sent: 3, Unsent: []int{1, 3}, Event: msgs[1].Event, Err: errors.New("boom"),
		}
	}}
	err := batchBus(tr).PublishAll(context.Background(), fourEnvelopes())

	var partial *events.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v", err)
	}
	if partial.Sent != 1 {
		t.Errorf("Sent = %d, want 1 - everything below Unsent[0] is necessarily published", partial.Sent)
	}
}

// An impossible partial report becomes the whole batch unsent, and the error names the
// adapter.
func TestAnImpossiblePartialReportIsNormalisedAndTheAdapterNamed(t *testing.T) {
	cases := []struct {
		name   string
		report *events.PartialPublishError
		says   string
	}{
		{"index past the batch", &events.PartialPublishError{Unsent: []int{1, 9}}, "outside the batch of 4"},
		{"negative index", &events.PartialPublishError{Unsent: []int{-1}}, "outside the batch of 4"},
		{"not ascending", &events.PartialPublishError{Unsent: []int{3, 1}}, "not ascending"},
		{"no unsent at all", &events.PartialPublishError{Unsent: nil}, "names no unsent envelope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report := *c.report
			report.Err = errors.New("underlying")
			tr := &batchTransport{report: func([]*events.Message) error { return &report }}
			err := batchBus(tr).PublishAll(context.Background(), fourEnvelopes())

			var partial *events.PartialPublishError
			if !errors.As(err, &partial) {
				t.Fatalf("err = %v", err)
			}
			if len(partial.Unsent) != 4 || partial.Sent != 0 {
				t.Errorf("Sent/Unsent = %d/%v, want the whole batch treated as unsent",
					partial.Sent, partial.Unsent)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error does not say what was impossible (%q): %v", c.says, err)
			}
			if !strings.Contains(err.Error(), "reported an impossible partial publish") {
				t.Errorf("error does not name the adapter as the source: %v", err)
			}
			if !errors.Is(err, report.Err) {
				t.Errorf("the underlying failure is no longer reachable: %v", err)
			}
		})
	}
}

// A bare error from a batch transport passes through untouched.
func TestABareBatchErrorIsNotTreatedAsPartial(t *testing.T) {
	boom := errors.New("connection refused")
	tr := &batchTransport{report: func([]*events.Message) error { return boom }}
	err := batchBus(tr).PublishAll(context.Background(), fourEnvelopes())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the adapter's own error", err)
	}
	var partial *events.PartialPublishError
	if errors.As(err, &partial) {
		t.Error("a bare error must not be dressed up as a partial publish")
	}
}

// UnsentFrom reports every index from i onward, with Sent and Event from the first.
func TestUnsentFromReportsTheContiguousTail(t *testing.T) {
	msgs := []*events.Message{
		{Event: "a.One"}, {Event: "b.Two"}, {Event: "a.Three"}, {Event: "b.Four"},
	}
	got := events.UnsentFrom(2, msgs, errors.New("broker rejected"))
	if want := []int{2, 3}; !reflect.DeepEqual(got.Unsent, want) {
		t.Errorf("Unsent = %v, want %v", got.Unsent, want)
	}
	if got.Sent != 2 {
		t.Errorf("Sent = %d, want 2 - the leading published run", got.Sent)
	}
	if got.Event != "a.Three" {
		t.Errorf("Event = %q, want the first unsent envelope's contract", got.Event)
	}
}

// UnsentAt sorts scattered indices and derives Sent and Event from the lowest.
func TestUnsentAtReportsScatteredIndicesAndSortsThem(t *testing.T) {
	msgs := []*events.Message{
		{Event: "a.One"}, {Event: "b.Two"}, {Event: "a.Three"}, {Event: "b.Four"},
	}
	got := events.UnsentAt([]int{3, 1}, msgs, errors.New("topic b unavailable"))
	if want := []int{1, 3}; !reflect.DeepEqual(got.Unsent, want) {
		t.Errorf("Unsent = %v, want %v sorted ascending", got.Unsent, want)
	}
	if got.Sent != 1 || got.Event != "b.Two" {
		t.Errorf("Sent/Event = %d/%q, want 1/b.Two - both derived from the first unsent", got.Sent, got.Event)
	}
}

// UnsentAt leaves the caller's slice unsorted.
func TestUnsentAtLeavesTheCallersSliceAlone(t *testing.T) {
	msgs := []*events.Message{{Event: "a"}, {Event: "b"}, {Event: "c"}, {Event: "d"}}
	mine := []int{3, 1}
	events.UnsentAt(mine, msgs, errors.New("boom"))
	if !reflect.DeepEqual(mine, []int{3, 1}) {
		t.Errorf("caller's slice = %v, want [3 1] untouched", mine)
	}
}

// Both constructors give Sent == Unsent[0], an ascending Unsent, and the first unsent
// envelope's Event.
func TestTheConstructorsSatisfyTheInvariants(t *testing.T) {
	msgs := []*events.Message{{Event: "a"}, {Event: "b"}, {Event: "c"}, {Event: "d"}}
	for name, got := range map[string]*events.PartialPublishError{
		"UnsentFrom": events.UnsentFrom(1, msgs, errors.New("x")),
		"UnsentAt":   events.UnsentAt([]int{2, 1}, msgs, errors.New("x")),
	} {
		if len(got.Unsent) == 0 {
			t.Fatalf("%s: no unsent indices", name)
		}
		if got.Sent != got.Unsent[0] {
			t.Errorf("%s: Sent = %d, want Unsent[0] = %d", name, got.Sent, got.Unsent[0])
		}
		if got.Event != msgs[got.Unsent[0]].Event {
			t.Errorf("%s: Event = %q, want %q", name, got.Event, msgs[got.Unsent[0]].Event)
		}
		for i := 1; i < len(got.Unsent); i++ {
			if got.Unsent[i] <= got.Unsent[i-1] {
				t.Errorf("%s: Unsent = %v is not ascending", name, got.Unsent)
			}
		}
	}
}

// A constructor given an index outside the batch does not panic.
func TestAConstructorWithAnOutOfRangeIndexDoesNotPanic(t *testing.T) {
	msgs := []*events.Message{{Event: "a"}}
	if got := events.UnsentAt([]int{9}, msgs, errors.New("x")); got.Event != "" {
		t.Errorf("Event = %q, want empty for an index outside the batch", got.Event)
	}
	if got := events.UnsentFrom(5, msgs, errors.New("x")); len(got.Unsent) != 0 {
		t.Errorf("Unsent = %v, want empty when the batch ends before i", got.Unsent)
	}
}

// ctxDeafPublisher ignores ctx.
type ctxDeafPublisher struct {
	published int
	batched   int
}

func (p *ctxDeafPublisher) Publish(context.Context, *events.Message) error {
	p.published++
	return nil
}

func (p *ctxDeafPublisher) PublishBatch(_ context.Context, msgs []*events.Message) error {
	p.batched += len(msgs)
	return nil
}

// PublishAll with a cancelled context publishes nothing, on the batch and one-at-a-time
// paths alike.
func TestPublishAllRefusesAnAlreadyCancelledContext(t *testing.T) {
	envs := []events.Envelope{
		{Event: "orders.OrderPlaced", Payload: map[string]string{"id": "o-1"}},
		{Event: "orders.OrderPlaced", Payload: map[string]string{"id": "o-2"}},
	}

	t.Run("batch upgrade", func(t *testing.T) {
		p := &ctxDeafPublisher{}
		bus := events.New(events.WithPublisher(p), events.WithCodec(codecjson.Codec{}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := bus.PublishAll(ctx, envs)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		var partial *events.PartialPublishError
		if errors.As(err, &partial) {
			t.Errorf("err is a partial report (%v), but nothing was published", partial)
		}
		if p.batched != 0 {
			t.Errorf("the transport took %d messages, want 0", p.batched)
		}
	})

	t.Run("one at a time", func(t *testing.T) {
		p := &publisherOnly{inner: &ctxDeafPublisher{}}
		bus := events.New(events.WithPublisher(p), events.WithCodec(codecjson.Codec{}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := bus.PublishAll(ctx, envs); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if p.inner.published != 0 {
			t.Errorf("the transport took %d messages, want 0", p.inner.published)
		}
	})
}

// publisherOnly hides the batch upgrade, so the bus takes its
// one-at-a-time fallback.
type publisherOnly struct{ inner *ctxDeafPublisher }

func (p *publisherOnly) Publish(ctx context.Context, msg *events.Message) error {
	return p.inner.Publish(ctx, msg)
}

// PublishAll on a live context reaches the transport.
func TestPublishAllStillPublishesOnALiveContext(t *testing.T) {
	p := &ctxDeafPublisher{}
	bus := events.New(events.WithPublisher(p), events.WithCodec(codecjson.Codec{}))
	err := bus.PublishAll(context.Background(), []events.Envelope{
		{Event: "orders.OrderPlaced", Payload: map[string]string{"id": "o-1"}},
	})
	if err != nil {
		t.Fatalf("publish all: %v", err)
	}
	if p.batched != 1 {
		t.Errorf("the transport took %d messages, want 1", p.batched)
	}
}

// Publish with a cancelled context publishes nothing, even on a transport that ignores
// ctx.
func TestPublishRefusesAnAlreadyCancelledContext(t *testing.T) {
	tr := memory.New()
	var delivered atomic.Int64
	if err := tr.Subscribe(context.Background(), []events.Subscription{{
		Event: "orders.OrderPlaced", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error {
			delivered.Add(1)
			return nil
		},
	}}); err != nil {
		t.Fatal(err)
	}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := bus.Publish(ctx, "orders.OrderPlaced", payload{ID: "o-1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	tr.Drain()
	if n := delivered.Load(); n != 0 {
		t.Errorf("the handler ran %d times, want 0", n)
	}
}

// Publish on a live context reaches the transport.
func TestPublishStillPublishesOnALiveContext(t *testing.T) {
	tr := memory.New()
	var delivered atomic.Int64
	if err := tr.Subscribe(context.Background(), []events.Subscription{{
		Event: "orders.OrderPlaced", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error {
			delivered.Add(1)
			return nil
		},
	}}); err != nil {
		t.Fatal(err)
	}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := bus.Publish(context.Background(), "orders.OrderPlaced", payload{ID: "o-1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()
	if n := delivered.Load(); n != 1 {
		t.Errorf("the handler ran %d times, want 1", n)
	}
}
