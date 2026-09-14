package events_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
)

// busWith builds a bus over a recording transport and the given chain.
func busWith(mws ...events.Middleware) (*events.Bus, *recordingTransport) {
	tr := &recordingTransport{}
	return events.New(
		events.WithTransport(tr),
		events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(mws...),
	), tr
}

// registered subscribes sub and returns the handler the transport was
// handed - the one a delivery goroutine calls.
func registered(t *testing.T, bus *events.Bus, tr *recordingTransport, sub events.Subscription) events.Handler {
	t.Helper()
	if err := bus.Subscribe(context.Background(), sub); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return tr.subs[len(tr.subs)-1].Handle
}

// panickingSub is a subscription whose handler panics.
func panickingSub(value any) events.Subscription {
	return events.Subscription{
		Event: "x.Y", Consumer: "C1", Group: "g",
		Handle: func(context.Context, *events.Message) error { panic(value) },
	}
}

// The chain on the bus wraps every subscription that reaches the
// transport, outermost first.
func TestBusMiddlewareWrapsEverySubscription(t *testing.T) {
	var trace string
	bus, tr := busWith(tagMW(&trace, "A"), tagMW(&trace, "B"), tagMW(&trace, "C"))
	h := registered(t, bus, tr, tracingSub(&trace, "x.Y", "C1", "g"))
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if want := ">A>B>C|H|<C<B<A"; trace != want {
		t.Errorf("chain order = %q, want %q (outermost-first)", trace, want)
	}
}

// WithMiddleware is what a hand-built slice inherits too: the demo's
// cross-design consumer is appended to a generated slice and never passes
// through any SubscribeAll, so the bus is the only seam that sees it.
func TestBusMiddlewareCoversAHandBuiltSubscription(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	record := func(sub events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			mu.Lock()
			seen = append(seen, sub.Consumer)
			mu.Unlock()
			return next(ctx, msg)
		}
	}
	var trace string
	bus, tr := busWith(record)
	subs := []events.Subscription{
		tracingSub(&trace, "x.Y", "Generated", "g1"),
		tracingSub(&trace, "other.Z", "HandWritten", "g2"),
	}
	if err := bus.SubscribeAll(context.Background(), subs); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	for _, sub := range tr.subs {
		if err := sub.Handle(context.Background(), &events.Message{Event: sub.Event}); err != nil {
			t.Fatalf("deliver %s: %v", sub.Consumer, err)
		}
	}

	if got := strings.Join(seen, ","); got != "Generated,HandWritten" {
		t.Errorf("middleware saw %q, want both subscriptions", got)
	}
}

// A bus with no middleware is the wrap it has always applied: one recover
// around the handler, nothing else.
func TestBusWithoutMiddlewareIsUnchanged(t *testing.T) {
	tr := &recordingTransport{}
	bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
	var trace string
	h := registered(t, bus, tr, tracingSub(&trace, "x.Y", "C1", "g"))
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if trace != "|H|" {
		t.Errorf("trace = %q, want %q", trace, "|H|")
	}

	err := registered(t, bus, tr, panickingSub("boom"))(context.Background(), &events.Message{Event: "x.Y"})
	var panicked *events.PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("got %v, want a *PanicError", err)
	}
}

// The rule, with no chain entry to remember: a panicking handler reaches
// the project's own middleware as an error, and the process survives.
func TestPanickingHandlerReachesTheProjectsMiddleware(t *testing.T) {
	var trace string
	var seen error
	observe := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			trace += ">M"
			err := next(ctx, msg)
			trace += "<M"
			seen = err
			return err
		}
	}
	bus, tr := busWith(observe)
	err := registered(t, bus, tr, panickingSub("boom"))(context.Background(), &events.Message{Event: "x.Y"})

	// The middleware ran either side of the panic rather than unwinding.
	if trace != ">M<M" {
		t.Errorf("trace = %q, want %q - the panic unwound past the middleware", trace, ">M<M")
	}
	var panicked *events.PanicError
	if !errors.As(seen, &panicked) {
		t.Fatalf("middleware saw %v, want a *PanicError", seen)
	}
	if panicked.Event != "x.Y" || panicked.Consumer != "C1" || panicked.Group != "g" {
		t.Errorf("PanicError names %s/%s/%s", panicked.Event, panicked.Consumer, panicked.Group)
	}
	if panicked.Value != "boom" || len(panicked.Stack) == 0 {
		t.Errorf("PanicError = {%v, stack %d bytes}", panicked.Value, len(panicked.Stack))
	}
	// The transport sees the same error the middleware returned.
	var fromTransport *events.PanicError
	if !errors.As(err, &fromTransport) || fromTransport != panicked {
		t.Errorf("transport saw %v, want the *PanicError the middleware saw", err)
	}
}

// A panicking MIDDLEWARE cannot end the process either. It sits above the
// inner recover, so only the outer one can catch it.
func TestPanickingMiddlewareDoesNotEndTheProcess(t *testing.T) {
	blowUp := func(events.Subscription, events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { panic("middleware blew up") }
	}
	var trace string
	bus, tr := busWith(blowUp)
	err := registered(t, bus, tr, tracingSub(&trace, "x.Y", "C1", "g"))(context.Background(), &events.Message{Event: "x.Y"})

	var panicked *events.PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("got %v, want a *PanicError", err)
	}
	if panicked.Value != "middleware blew up" {
		t.Errorf("PanicError.Value = %v, want the middleware's panic", panicked.Value)
	}
	if trace != "" {
		t.Errorf("the handler ran despite the middleware panicking: trace %q", trace)
	}
}

// Recovery on both sides of the chain must still build exactly one
// PanicError per panic: once the inner one catches, no panic is in flight,
// so the outer recover returns nil and passes the error through.
func TestExactlyOnePanicErrorPerPanic(t *testing.T) {
	cases := []struct {
		name      string
		chain     []events.Middleware
		sub       events.Subscription
		wantValue any
	}{
		{"handler panics", []events.Middleware{passthrough()}, panickingSub("boom"), "boom"},
		{"middleware panics", []events.Middleware{panicking("middleware blew up")}, panickingSub("boom"), "middleware blew up"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus, tr := busWith(c.chain...)
			err := registered(t, bus, tr, c.sub)(context.Background(), &events.Message{Event: "x.Y"})

			var panicked *events.PanicError
			if !errors.As(err, &panicked) {
				t.Fatalf("got %v, want a *PanicError", err)
			}
			// A second one could only arrive by one recover re-wrapping the
			// other, which would leave the first as the panic value.
			if inner, ok := panicked.Value.(*events.PanicError); ok {
				t.Errorf("two PanicErrors for one panic; the outer recover wrapped %v", inner)
			}
			if panicked.Value != c.wantValue {
				t.Errorf("PanicError.Value = %v, want %v", panicked.Value, c.wantValue)
			}
		})
	}
}

// passthrough is a middleware that only delegates - enough to put a chain
// on the bus, and so a second recover around it.
func passthrough() events.Middleware {
	return func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error { return next(ctx, msg) }
	}
}

// panicking is a middleware that panics instead of delegating.
func panicking(value any) events.Middleware {
	return func(events.Subscription, events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { panic(value) }
	}
}

// The whole point of recovery is that the process lives. Delivery runs on
// a goroutine the transport spawns, so this drives a real one.
func TestDeliveryGoroutineSurvivesBothPanics(t *testing.T) {
	var mu sync.Mutex
	var reported []error
	tr := memory.New(memory.WithErrorHandler(func(_ events.Subscription, _ *events.Message, err error) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	}))
	bus := events.New(
		events.WithTransport(tr),
		events.WithCodec(codecjson.Codec{}),
		events.WithMiddleware(passthrough(), panicking("from the chain")),
	)
	if err := bus.Subscribe(context.Background(), panickingSub("from the handler")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(context.Background(), "x.Y", map[string]string{}, events.WithKey("k")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 {
		t.Fatalf("error handler saw %d errors, want 1", len(reported))
	}
	var panicked *events.PanicError
	if !errors.As(reported[0], &panicked) {
		t.Fatalf("reported %v, want a *PanicError", reported[0])
	}
	// The chain panics before reaching the handler, so that is the one
	// caught - by the outer recover, the only one above it.
	if panicked.Value != "from the chain" {
		t.Errorf("PanicError.Value = %v, want the chain's panic", panicked.Value)
	}
}

// A chain folded by Apply is wrapped by the bus from outside, as one
// opaque handler - so a handler panic unwinds past it unless Recover sits
// at its innermost end. That is the gap Recover exists for.
func TestRecoverGivesAHandAppliedChainWhatABusChainGetsFree(t *testing.T) {
	for _, c := range []struct {
		name     string
		chain    events.Chain
		wantSeen bool
	}{
		{"without Recover", events.NewChain(nil), false},
		{"with Recover innermost", events.NewChain(events.Recover()), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var seen error
			observe := func(_ events.Subscription, next events.Handler) events.Handler {
				return func(ctx context.Context, msg *events.Message) error {
					err := next(ctx, msg)
					seen = err
					return err
				}
			}
			tr := &recordingTransport{}
			bus := events.New(events.WithTransport(tr), events.WithCodec(codecjson.Codec{}))
			subs := events.NewChain(observe).Append(c.chain...).Apply([]events.Subscription{panickingSub("boom")})
			if err := bus.SubscribeAll(context.Background(), subs); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			err := tr.subs[0].Handle(context.Background(), &events.Message{Event: "x.Y"})
			var panicked *events.PanicError
			// The bus reports it either way; only visibility differs.
			if !errors.As(err, &panicked) {
				t.Fatalf("transport saw %v, want a *PanicError", err)
			}
			if inner, ok := panicked.Value.(*events.PanicError); ok {
				t.Errorf("two PanicErrors for one panic; the bus wrapped %v", inner)
			}
			if got := errors.As(seen, &panicked); got != c.wantSeen {
				t.Errorf("hand-applied middleware saw the panic = %v, want %v (saw %v)", got, c.wantSeen, seen)
			}
		})
	}
}
