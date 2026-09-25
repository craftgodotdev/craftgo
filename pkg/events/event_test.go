package events_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

var (
	orderPlaced  = events.NewEvent[order]("orders.Placed", (*order).Validate)
	orderShipped = events.NewEvent[order]("orders.Shipped", nil)
	// orderBatch is an array-payload contract as codegen writes it, validating each element.
	orderBatch = events.NewEvent[[]order]("orders.Batch", validateOrderBatch)
)

func validateOrderBatch(items *[]order) error {
	for i := range *items {
		if err := (*items)[i].Validate(); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}
	return nil
}

func TestTheDescriptorCarriesItsContract(t *testing.T) {
	if got := orderPlaced.Contract(); got != "orders.Placed" {
		t.Errorf("Contract() = %q", got)
	}
}

// A descriptor round-trips its payload through the bus.
func TestADescriptorRoundTripsItsPayload(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *order, 1)
	start(t, context.Background(), bus, orderPlaced.Subscription(bus, "receipts",
		func(_ context.Context, placed *order) error {
			got <- placed
			return nil
		}))

	if err := orderPlaced.Publish(context.Background(), bus, &order{ID: "o-1", Count: 7}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	select {
	case p := <-got:
		if p.ID != "o-1" || p.Count != 7 {
			t.Errorf("delivered %+v", p)
		}
	default:
		t.Fatal("nothing delivered")
	}
}

// A descriptor publishes the same message Bus.Publish does.
func TestADescriptorPublishesWhatTheBusWouldHave(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	placed := &order{ID: "o-1", Count: 2}
	if err := orderPlaced.Publish(context.Background(), bus, placed, events.WithKey("o-1")); err != nil {
		t.Fatalf("descriptor publish: %v", err)
	}
	if err := bus.Publish(context.Background(), "orders.Placed", placed, events.WithKey("o-1")); err != nil {
		t.Fatalf("bus publish: %v", err)
	}
	if len(tr.sent) != 2 {
		t.Fatalf("sent %d messages, want 2", len(tr.sent))
	}
	if !reflect.DeepEqual(tr.sent[0], tr.sent[1]) {
		t.Errorf("descriptor sent %+v, bus sent %+v", tr.sent[0], tr.sent[1])
	}
}

// A payload that does not validate is a *PayloadError and is not published.
func TestAnInvalidPayloadIsNotPublished(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	err := orderPlaced.Publish(context.Background(), bus, &order{Count: 1})
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if payloadErr.Event != "orders.Placed" {
		t.Errorf("the error does not name the contract: %+v", payloadErr)
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("the payload type's own reason is lost: %v", err)
	}
	if len(tr.sent) != 0 {
		t.Errorf("%d messages went out", len(tr.sent))
	}
}

func TestPublishingNoPayloadIsAPayloadError(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	err := orderPlaced.Publish(context.Background(), bus, nil)
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if len(tr.sent) != 0 {
		t.Errorf("%d messages went out", len(tr.sent))
	}
}

// A descriptor with no validator publishes any payload.
func TestADescriptorWithoutValidationPublishesAnything(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	if err := orderShipped.Publish(context.Background(), bus, &order{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(tr.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(tr.sent))
	}
}

// A descriptor typed on a slice round-trips an array payload whole.
func TestADescriptorRoundTripsAnArrayPayload(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *[]order, 1)
	start(t, context.Background(), bus, orderBatch.Subscription(bus, "receipts",
		func(_ context.Context, batch *[]order) error {
			got <- batch
			return nil
		}))

	if err := orderBatch.Publish(context.Background(), bus, &[]order{{ID: "o-1", Count: 1}, {ID: "o-2", Count: 2}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	select {
	case batch := <-got:
		if want := []order{{ID: "o-1", Count: 1}, {ID: "o-2", Count: 2}}; !reflect.DeepEqual(*batch, want) {
			t.Errorf("delivered %+v, want %+v", *batch, want)
		}
	default:
		t.Fatal("nothing delivered")
	}
}

// One invalid element fails the whole publish with a *PayloadError naming its index.
func TestAnInvalidElementIsAPayloadError(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	err := orderBatch.Publish(context.Background(), bus, &[]order{{ID: "o-1"}, {Count: 3}})
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if payloadErr.Event != "orders.Batch" {
		t.Errorf("the error does not name the contract: %+v", payloadErr)
	}
	if !strings.Contains(err.Error(), "item 1") || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("the error names neither the element nor its reason: %v", err)
	}
	if len(tr.sent) != 0 {
		t.Errorf("%d messages went out", len(tr.sent))
	}
}

// An array with one invalid element never reaches the typed handler.
func TestAnInvalidElementNeverReachesTheHandler(t *testing.T) {
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	ran := false
	h := orderBatch.Handler(bus, func(context.Context, *[]order) error {
		ran = true
		return nil
	})

	err := h(context.Background(), &events.Message{
		Event:   "orders.Batch",
		Payload: []byte(`[{"id":"o-1"},{"count":3}]`),
	})
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if !strings.Contains(err.Error(), "item 1") {
		t.Errorf("the failing element is not named: %v", err)
	}
	if ran {
		t.Error("the typed handler ran on a batch that did not validate")
	}
}

// deliverTo runs one message through a descriptor's handler and reports
// what the handler answered and whether the typed function ran.
func deliverTo(t *testing.T, msg *events.Message) (bool, error) {
	t.Helper()
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	ran := false
	h := orderPlaced.Handler(bus, func(context.Context, *order) error {
		ran = true
		return nil
	})
	err := h(context.Background(), msg)
	return ran, err
}

// A payload that decodes and validates reaches the typed handler.
func TestAGoodPayloadReachesTheHandler(t *testing.T) {
	ran, err := deliverTo(t, &events.Message{Event: "orders.Placed", Payload: []byte(`{"id":"o-1"}`)})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if !ran {
		t.Error("the typed handler did not run")
	}
}

// An undecodable payload is a *PayloadError and never reaches the typed handler.
func TestAnUndecodablePayloadIsAPayloadError(t *testing.T) {
	ran, err := deliverTo(t, &events.Message{Event: "orders.Placed", Payload: []byte("{")})
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if ran {
		t.Error("the typed handler ran on a payload that did not decode")
	}
}

func TestAPayloadThatFailsValidationNeverReachesTheHandler(t *testing.T) {
	ran, err := deliverTo(t, &events.Message{Event: "orders.Placed", Payload: []byte(`{"count":1}`)})
	var payloadErr *events.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("the payload type's own reason is lost: %v", err)
	}
	if ran {
		t.Error("the typed handler ran on a payload that did not validate")
	}
}

// A message stamped with another codec is ErrCodecMismatch, not a *PayloadError.
func TestAForeignCodecIsNotAPayloadError(t *testing.T) {
	ran, err := deliverTo(t, &events.Message{
		Event:    "orders.Placed",
		Payload:  []byte(`{"id":"o-1"}`),
		Metadata: map[string]string{events.MetaCodec: "protobuf"},
	})
	if !errors.Is(err, events.ErrCodecMismatch) {
		t.Fatalf("err = %v, want ErrCodecMismatch", err)
	}
	var payloadErr *events.PayloadError
	if errors.As(err, &payloadErr) {
		t.Error("a codec mismatch is a configuration error, not a poison payload")
	}
	if ran {
		t.Error("the typed handler ran on a message it could not read")
	}
}

// The typed handler's own error passes through unwrapped.
func TestTheTypedHandlersErrorIsPassedThrough(t *testing.T) {
	bus := events.New(events.WithCodec(codecjson.Codec{}))
	boom := errors.New("downstream unavailable")
	h := orderPlaced.Handler(bus, func(context.Context, *order) error { return boom })

	err := h(context.Background(), &events.Message{Event: "orders.Placed", Payload: []byte(`{"id":"o-1"}`)})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the handler's own", err)
	}
	var payloadErr *events.PayloadError
	if errors.As(err, &payloadErr) {
		t.Error("a handler failure is not a payload failure")
	}
}

// A descriptor's subscription names its consumer after the contract and carries no chain.
func TestASubscriptionDefaultsItsConsumerToTheContract(t *testing.T) {
	bus, _ := busOver()
	sub := orderPlaced.Subscription(bus, "receipts", func(context.Context, *order) error { return nil })

	if sub.Consumer != orderPlaced.Contract() {
		t.Errorf("Consumer = %q, want the contract %q", sub.Consumer, orderPlaced.Contract())
	}
	if sub.Chain != nil {
		t.Errorf("Chain = %v, want none - the chain is the bus's", sub.Chain)
	}
	if err := bus.Register(sub); err != nil {
		t.Fatalf("register: %v", err)
	}
	want := events.Plan{Groups: []events.PlanGroup{{
		Name:      "receipts",
		Consumers: []events.PlanConsumer{{Event: "orders.Placed", Consumer: "orders.Placed"}},
	}}}
	if got := bus.Plan(); !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %+v, want %+v", got, want)
	}
}

// A Consumer and Chain set on a descriptor's subscription take effect.
func TestADescriptorBuildsTheSubscription(t *testing.T) {
	bus, tr := busOver()
	var trace string
	sub := orderPlaced.Subscription(bus, "receipts",
		func(context.Context, *order) error {
			trace += "|H|"
			return nil
		})
	sub.Consumer = "SendReceipt"
	sub.Chain = events.NewChain(tagMW(&trace, "S"))
	if sub.Event != "orders.Placed" || sub.Consumer != "SendReceipt" || sub.Group != "receipts" {
		t.Fatalf("subscription = %+v", sub)
	}
	start(t, context.Background(), bus, sub)

	msg := &events.Message{Event: "orders.Placed", Payload: []byte(`{"id":"o-1"}`)}
	if err := tr.subs[0].Handle(context.Background(), msg); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if want := ">S|H|<S"; trace != want {
		t.Errorf("trace = %q, want %q - the subscription's chain was not applied", trace, want)
	}
}
