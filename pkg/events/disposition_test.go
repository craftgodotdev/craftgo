package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// directTransport delivers synchronously, handing the handler the published message itself.
type directTransport struct {
	subs []events.Subscription
	// handlerErr is what the last delivery returned.
	handlerErr error
}

func (d *directTransport) Subscribe(_ context.Context, subs []events.Subscription) error {
	d.subs = append(d.subs, subs...)
	return nil
}

func (d *directTransport) Publish(ctx context.Context, msg *events.Message) error {
	for _, sub := range d.subs {
		if sub.Event == msg.Event {
			d.handlerErr = sub.Handle(ctx, msg)
		}
	}
	return nil
}

// dispositionTransport honours a fixed set of dispositions.
type dispositionTransport struct {
	directTransport
	can map[events.Disposition]bool
}

func (d *dispositionTransport) CanDisposition(want events.Disposition) bool { return d.can[want] }

// deliverThrough delivers one message to h behind chain and returns it once the chain
// has finished.
func deliverThrough(t *testing.T, chain events.Chain, h events.Handler) *events.Message {
	t.Helper()
	return deliverOn(t, &directTransport{}, chain, h)
}

// deliverOn is deliverThrough over tr.
func deliverOn(t *testing.T, tr interface {
	events.Publisher
	events.Subscriber
}, chain events.Chain, h events.Handler) *events.Message {
	t.Helper()
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(chain...))
	start(t, context.Background(), bus, events.Subscription{
		Event: "x.Y", Consumer: "C", Group: "g", Handle: h,
	})
	msg := &events.Message{Event: "x.Y", Payload: []byte("{}")}
	if err := tr.Publish(context.Background(), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return msg
}

// canRedeliver is a transport that honours settle and redeliver.
func canRedeliver() *dispositionTransport {
	return &dispositionTransport{can: map[events.Disposition]bool{
		events.DispositionSettle: true, events.DispositionRedeliver: true,
	}}
}

// A panic escaping the chain asks for redelivery on a transport that can redeliver.
func TestAPanicEscapingTheChainAsksForRedelivery(t *testing.T) {
	blowUp := func(events.Subscription, events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { panic("middleware blew up") }
	}
	var ran bool
	tr := canRedeliver()
	msg := deliverOn(t, tr, events.NewChain(blowUp),
		func(context.Context, *events.Message) error { ran = true; return nil })

	var panicked *events.PanicError
	if !errors.As(tr.handlerErr, &panicked) {
		t.Fatalf("the transport saw %v, want a *PanicError", tr.handlerErr)
	}
	if panicked.Value != "middleware blew up" {
		t.Errorf("PanicError.Value = %v, want the middleware's panic", panicked.Value)
	}
	if got := msg.Disposition(); got != events.DispositionRedeliver {
		t.Errorf("disposition after a panic in a middleware = %v, want redeliver - unset settles and the message is gone", got)
	}
	if ran {
		t.Error("the handler ran despite the middleware panicking")
	}
}

// A panic escaping the chain leaves the disposition unset on a settle-only transport.
func TestAPanicEscapingTheChainLeavesASettleOnlyTransportUnset(t *testing.T) {
	blowUp := func(events.Subscription, events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { panic("middleware blew up") }
	}
	msg := deliverOn(t, &directTransport{}, events.NewChain(blowUp),
		func(context.Context, *events.Message) error { return nil })
	if got := msg.Disposition(); got != events.DispositionUnset {
		t.Errorf("disposition = %v, want unset on a transport that cannot redeliver", got)
	}
}

// A panicking handler leaves the disposition unset for the chain to decide, even where
// redelivery is available.
func TestAPanickingHandlerLeavesTheDecisionToTheChain(t *testing.T) {
	var seen error
	watch := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			seen = next(ctx, msg)
			return seen
		}
	}
	msg := deliverOn(t, canRedeliver(), events.NewChain(watch),
		func(context.Context, *events.Message) error { panic("handler blew up") })

	var panicked *events.PanicError
	if !errors.As(seen, &panicked) {
		t.Fatalf("the middleware saw %v, want a *PanicError", seen)
	}
	if got := msg.Disposition(); got != events.DispositionUnset {
		t.Errorf("disposition = %v, want unset - the chain above the handler decides", got)
	}
}

// subscribeWith registers one handler on a bus built from opts and starts it, returning
// the first error.
func subscribeWith(tr interface {
	events.Publisher
	events.Subscriber
}, opts ...events.Option) error {
	opts = append([]events.Option{events.WithTransport(tr), events.WithCodec(codecjson.Codec{})}, opts...)
	bus := events.New(opts...)
	if err := bus.Register(events.Subscription{
		Event: "x.Y", Consumer: "C", Group: "g",
		Handle: func(context.Context, *events.Message) error { return nil },
	}); err != nil {
		return err
	}
	return bus.Start(context.Background())
}

func TestTheZeroDispositionIsUnset(t *testing.T) {
	var msg events.Message
	if got := msg.Disposition(); got != events.DispositionUnset {
		t.Errorf("zero disposition = %v, want unset", got)
	}
	if got := msg.Deliveries(); got != 0 {
		t.Errorf("deliveries = %d, want 0", got)
	}
}

// The outermost middleware writes the disposition last, and may change or clear it.
func TestTheLastWriterWinsAndMayClear(t *testing.T) {
	inner := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			err := next(ctx, msg)
			msg.Redeliver()
			return err
		}
	}
	outer := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			err := next(ctx, msg)
			if msg.Disposition() == events.DispositionRedeliver {
				msg.Reject()
			}
			return err
		}
	}
	ok := func(context.Context, *events.Message) error { return nil }

	msg := deliverThrough(t, events.NewChain(outer, inner), ok)
	if got := msg.Disposition(); got != events.DispositionReject {
		t.Errorf("disposition = %v, want the outermost middleware's reject", got)
	}

	clearing := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			err := next(ctx, msg)
			msg.Settle()
			return err
		}
	}
	msg = deliverThrough(t, events.NewChain(clearing, inner), ok)
	if got := msg.Disposition(); got != events.DispositionSettle {
		t.Errorf("disposition = %v, want settle - the outermost writer may clear", got)
	}
}

// A panic voids the disposition asked for beneath it.
func TestAPanicVoidsTheDispositionAskedForBeneathIt(t *testing.T) {
	stale := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			msg.Redeliver()
			return next(ctx, msg)
		}
	}
	msg := deliverThrough(t, events.NewChain(stale),
		func(context.Context, *events.Message) error { panic("boom") })
	if got := msg.Disposition(); got != events.DispositionUnset {
		t.Errorf("disposition after a panic = %v, want unset", got)
	}
}

// Deliveries returns what SetDeliveries recorded.
func TestDeliveriesIsWhatTheTransportRecorded(t *testing.T) {
	var msg events.Message
	msg.SetDeliveries(3)
	if got := msg.Deliveries(); got != 3 {
		t.Errorf("deliveries = %d, want 3", got)
	}
}

// A transport without Dispositioner honours settle and nothing else.
func TestATransportWithNoCapabilityHonoursSettleAlone(t *testing.T) {
	if err := subscribeWith(&directTransport{},
		events.WithDispositionRequired(events.DispositionSettle)); err != nil {
		t.Errorf("settle must be available everywhere: %v", err)
	}

	for _, d := range []events.Disposition{events.DispositionRedeliver, events.DispositionReject} {
		err := subscribeWith(&directTransport{}, events.WithDispositionRequired(d))
		if !errors.Is(err, events.ErrDispositionUnsupported) {
			t.Errorf("requiring %v on a plain transport = %v, want ErrDispositionUnsupported", d, err)
		}
	}
}

// A required disposition the transport lacks fails Register, naming the disposition.
func TestARequiredDispositionTheTransportLacksFailsAtRegister(t *testing.T) {
	tr := &dispositionTransport{can: map[events.Disposition]bool{events.DispositionSettle: true}}

	err := subscribeWith(tr, events.WithDispositionRequired(events.DispositionRedeliver))
	if !errors.Is(err, events.ErrDispositionUnsupported) {
		t.Fatalf("subscribe = %v, want ErrDispositionUnsupported", err)
	}
	if len(tr.subs) != 0 {
		t.Error("the subscription was registered anyway")
	}
	if !strings.Contains(err.Error(), "redeliver") {
		t.Errorf("error does not name the disposition: %v", err)
	}
}

// Repeated WithDispositionRequired calls accumulate.
func TestRequiringTwoDispositionsChecksBoth(t *testing.T) {
	tr := &dispositionTransport{can: map[events.Disposition]bool{
		events.DispositionSettle: true, events.DispositionRedeliver: true,
	}}

	err := subscribeWith(tr,
		events.WithDispositionRequired(events.DispositionRedeliver),
		events.WithDispositionRequired(events.DispositionReject))
	if !errors.Is(err, events.ErrDispositionUnsupported) {
		t.Fatalf("subscribe = %v, want the second requirement to be checked too", err)
	}
	if !strings.Contains(err.Error(), "reject") {
		t.Errorf("error names the wrong disposition: %v", err)
	}
}

// A capability the transport has lets the subscription through.
func TestARequiredDispositionTheTransportHasSubscribes(t *testing.T) {
	tr := &dispositionTransport{can: map[events.Disposition]bool{
		events.DispositionSettle: true, events.DispositionRedeliver: true, events.DispositionReject: true,
	}}

	if err := subscribeWith(tr,
		events.WithDispositionRequired(events.DispositionRedeliver),
		events.WithDispositionRequired(events.DispositionReject)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if len(tr.subs) != 1 {
		t.Errorf("registered %d subscriptions, want 1", len(tr.subs))
	}
}
