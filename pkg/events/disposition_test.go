package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
)

// directTransport delivers synchronously and hands the handler the very
// message it was given, so a test can read back what the chain decided
// about that delivery.
type directTransport struct {
	subs []events.Subscription
}

func (d *directTransport) Subscribe(_ context.Context, sub events.Subscription) error {
	d.subs = append(d.subs, sub)
	return nil
}

func (d *directTransport) Publish(ctx context.Context, msg *events.Message) error {
	for _, sub := range d.subs {
		if sub.Event == msg.Event {
			_ = sub.Handle(ctx, msg)
		}
	}
	return nil
}

// dispositionTransport answers the capability query with a fixed set, the
// way an adapter built in one mode does.
type dispositionTransport struct {
	directTransport
	can map[events.Disposition]bool
}

func (d *dispositionTransport) CanDisposition(want events.Disposition) bool { return d.can[want] }

// deliverThrough subscribes h behind chain and delivers one message,
// returning it once the chain has finished with it.
func deliverThrough(t *testing.T, chain events.Chain, h events.Handler) *events.Message {
	t.Helper()
	tr := &directTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(chain...))
	if err := bus.Subscribe(context.Background(), events.Subscription{
		Event: "x.Y", Consumer: "C", Handle: h,
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	msg := &events.Message{Event: "x.Y", Payload: []byte("{}")}
	if err := tr.Publish(context.Background(), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return msg
}

// subscribeWith builds a bus from opts and subscribes one handler,
// returning what Subscribe answered.
func subscribeWith(tr interface {
	events.Publisher
	events.Subscriber
}, opts ...events.Option) error {
	opts = append([]events.Option{events.WithTransport(tr), events.WithCodec(codecjson.Codec{})}, opts...)
	return events.New(opts...).Subscribe(context.Background(), events.Subscription{
		Event: "x.Y", Consumer: "C",
		Handle: func(context.Context, *events.Message) error { return nil },
	})
}

func TestTheZeroDispositionIsUnset(t *testing.T) {
	var msg events.Message
	if got := msg.Disposition(); got != events.DispositionUnset {
		t.Errorf("zero disposition = %v, want unset", got)
	}
	if msg.Reached() {
		t.Error("a message nothing delivered has been reached")
	}
	if got := msg.Deliveries(); got != 0 {
		t.Errorf("deliveries = %d, want 0", got)
	}
}

// The chain returns innermost first, so the outermost middleware writes
// last - it can see what everything below asked for and change it.
// Clearing back to settle is allowed for the same reason.
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

// A frame that panicked did not finish deciding, so what it asked for is
// void. Without this a middleware that asked for redelivery and then
// panicked would be redelivered for ever with nothing left to stop it.
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

// Reached separates a message that failed from a chain that broke before
// the handler ran. A handler that panics has still been reached.
func TestReachedSaysWhetherTheHandlerWasEntered(t *testing.T) {
	msg := deliverThrough(t, nil, func(context.Context, *events.Message) error { return nil })
	if !msg.Reached() {
		t.Error("a delivered message was not marked reached")
	}

	msg = deliverThrough(t, nil, func(context.Context, *events.Message) error { panic("boom") })
	if !msg.Reached() {
		t.Error("a handler that panicked was not marked reached")
	}

	stops := func(_ events.Subscription, _ events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { return errors.New("chain refused") }
	}
	msg = deliverThrough(t, events.NewChain(stops),
		func(context.Context, *events.Message) error { return nil })
	if msg.Reached() {
		t.Error("a chain that never called the handler marked the message reached")
	}
}

// Deliveries comes from the transport, which is the only thing that knows.
func TestDeliveriesIsWhatTheTransportRecorded(t *testing.T) {
	var msg events.Message
	msg.SetDeliveries(3)
	if got := msg.Deliveries(); got != 3 {
		t.Errorf("deliveries = %d, want 3", got)
	}
}

// A transport that does not implement Dispositioner honours settle and
// nothing else, so an adapter with one mode needs no code to say so.
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

// The refusal is at subscribe, not at the first message: a chain that
// calls Redeliver on a transport that settles instead loses every message
// it meant to retry, and nothing reports it.
func TestARequiredDispositionTheTransportLacksFailsAtSubscribe(t *testing.T) {
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

// Repeated calls accumulate rather than replace, so asking for a second
// one cannot silently drop the first.
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
