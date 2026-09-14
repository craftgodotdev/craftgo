package events_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	events "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

// order is a payload type of the shape codegen produces: fields, and a
// Validate the descriptor is handed as a method expression.
type order struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func (o *order) Validate() error {
	if o.ID == "" {
		return errors.New("id is required")
	}
	return nil
}

var (
	orderPlaced  = events.NewEvent[order]("orders.Placed", (*order).Validate)
	orderShipped = events.NewEvent[order]("orders.Shipped", nil)
)

func TestTheDescriptorCarriesItsContract(t *testing.T) {
	if got := orderPlaced.Contract(); got != "orders.Placed" {
		t.Errorf("Contract() = %q", got)
	}
}

// The descriptor is the typed way round the untyped bus: publish a *T,
// consume a *T, with the encoding in between nobody's business.
func TestADescriptorRoundTripsItsPayload(t *testing.T) {
	tr := memory.New()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))

	got := make(chan *order, 1)
	start(t, context.Background(), bus, orderPlaced.Subscription(bus, "SendReceipt", "receipts", nil,
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

// The descriptor's publish is the bus's publish under the contract name,
// byte for byte: a consumer cannot tell which one sent the message.
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

// A payload that does not validate is refused where it is broken, and
// nothing goes out - the same failure a consumer would have reported once
// per subscriber.
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

// A descriptor without validation publishes whatever it is given: the
// generated one is nil for a payload type with no Validate.
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

// The control for the three tests below: a payload that decodes and
// validates does reach the typed handler.
func TestAGoodPayloadReachesTheHandler(t *testing.T) {
	ran, err := deliverTo(t, &events.Message{Event: "orders.Placed", Payload: []byte(`{"id":"o-1"}`)})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if !ran {
		t.Error("the typed handler did not run")
	}
}

// The same bytes fail the same way on every delivery, so a chain can pick
// a poison payload out and give the message up rather than retry it.
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

// A message stamped with another codec is a configuration mistake, not a
// poison payload: the same bytes decode once the two sides agree.
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

// A handler's own error is the handler's: nothing here dresses it up.
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

// The subscription a descriptor builds is the one the design declared:
// the contract, the consumer and group it was given, and the chain it was
// handed.
func TestADescriptorBuildsTheSubscription(t *testing.T) {
	bus, tr := busOver()
	var trace string
	sub := orderPlaced.Subscription(bus, "SendReceipt", "receipts", events.NewChain(tagMW(&trace, "S")),
		func(context.Context, *order) error {
			trace += "|H|"
			return nil
		})
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
