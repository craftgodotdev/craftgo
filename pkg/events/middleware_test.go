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

// wrapped registers sub on a bus carrying mws and returns the handler the transport was
// handed.
func wrapped(t *testing.T, sub events.Subscription, mws ...events.Middleware) events.Handler {
	t.Helper()
	bus, tr := busOver(events.WithMiddleware(mws...))
	start(t, context.Background(), bus, sub)
	return tr.subs[0].Handle
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
	h := wrapped(t, tracingSub(&trace, "x.Y", "C1", "g"),
		tagMW(&trace, "A"), tagMW(&trace, "B"), tagMW(&trace, "C"))
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if want := ">A>B>C|H|<C<B<A"; trace != want {
		t.Errorf("chain order = %q, want %q (outermost-first)", trace, want)
	}
}

// Use appends to the bus chain, which wraps subscriptions registered before it, outside
// their own chains.
func TestUseWrapsEverySubscriptionOutsideItsOwnChain(t *testing.T) {
	var trace string
	bus, tr := busOver(events.WithMiddleware(tagMW(&trace, "N")))
	sub := tracingSub(&trace, "x.Y", "C1", "g")
	sub.Chain = events.NewChain(tagMW(&trace, "S"))
	if err := errors.Join(bus.Register(sub), bus.Register(tracingSub(&trace, "other.Z", "C2", "g2"))); err != nil {
		t.Fatalf("register: %v", err)
	}
	bus.Use(tagMW(&trace, "U1"), tagMW(&trace, "U2"))
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := tr.subs[0].Handle(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if want := ">N>U1>U2>S|H|<S<U2<U1<N"; trace != want {
		t.Errorf("chain order = %q, want %q", trace, want)
	}

	trace = ""
	if err := tr.subs[1].Handle(context.Background(), &events.Message{Event: "other.Z"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if want := ">N>U1>U2|H|<U2<U1<N"; trace != want {
		t.Errorf("the second subscription ran %q, want %q", trace, want)
	}
}

// Use after Start panics with a message naming both methods.
func TestUseAfterStartPanics(t *testing.T) {
	var trace string
	bus, _ := busOver()
	start(t, context.Background(), bus, tracingSub(&trace, "x.Y", "C1", "g"))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Use after Start did not panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "Use") || !strings.Contains(msg, "Start") {
			t.Errorf("panic value = %v, want a message naming Use and Start", r)
		}
	}()
	bus.Use(passthrough())
}

// A subscription's own chain runs inside the bus chain.
func TestASubscriptionsChainRunsInsideTheBusChain(t *testing.T) {
	var trace string
	sub := tracingSub(&trace, "x.Y", "C1", "g")
	sub.Chain = events.NewChain(tagMW(&trace, "S1"), tagMW(&trace, "S2"))
	h := wrapped(t, sub, tagMW(&trace, "B1"), tagMW(&trace, "B2"))
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if want := ">B1>B2>S1>S2|H|<S2<S1<B2<B1"; trace != want {
		t.Errorf("chain order = %q, want %q", trace, want)
	}
}

// A subscription's own chain sees a panicking handler as a *PanicError, and so does the
// transport.
func TestRecoverySurroundsBothChains(t *testing.T) {
	var seen error
	observe := func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			err := next(ctx, msg)
			seen = err
			return err
		}
	}
	sub := panickingSub("boom")
	sub.Chain = events.NewChain(observe)
	err := wrapped(t, sub, passthrough())(context.Background(), &events.Message{Event: "x.Y"})

	var panicked *events.PanicError
	if !errors.As(seen, &panicked) {
		t.Fatalf("the subscription's own chain saw %v, want a *PanicError", seen)
	}
	if !errors.As(err, &panicked) {
		t.Fatalf("the transport saw %v, want a *PanicError", err)
	}
}

// The bus chain wraps every registered subscription, whoever built it.
func TestBusMiddlewareCoversEverySubscription(t *testing.T) {
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
	bus, tr := busOver(events.WithMiddleware(record))
	start(t, context.Background(), bus,
		tracingSub(&trace, "x.Y", "Generated", "g1"),
		tracingSub(&trace, "other.Z", "HandWritten", "g2"),
	)
	for _, sub := range tr.subs {
		if err := sub.Handle(context.Background(), &events.Message{Event: sub.Event}); err != nil {
			t.Fatalf("deliver %s: %v", sub.Consumer, err)
		}
	}

	if got := strings.Join(seen, ","); got != "Generated,HandWritten" {
		t.Errorf("middleware saw %q, want both subscriptions", got)
	}
}

// A bus with no middleware wraps the handler in a recover and nothing else.
func TestBusWithoutMiddlewareIsUnchanged(t *testing.T) {
	var trace string
	h := wrapped(t, tracingSub(&trace, "x.Y", "C1", "g"))
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if trace != "|H|" {
		t.Errorf("trace = %q, want %q", trace, "|H|")
	}

	err := wrapped(t, panickingSub("boom"))(context.Background(), &events.Message{Event: "x.Y"})
	var panicked *events.PanicError
	if !errors.As(err, &panicked) {
		t.Fatalf("got %v, want a *PanicError", err)
	}
}

// A panicking handler reaches the bus chain as the same *PanicError the transport sees.
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
	err := wrapped(t, panickingSub("boom"), observe)(context.Background(), &events.Message{Event: "x.Y"})

	// The middleware ran on both sides of the panic.
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

// A panicking middleware is caught by the outer recover, and the handler does not run.
func TestPanickingMiddlewareDoesNotEndTheProcess(t *testing.T) {
	blowUp := func(events.Subscription, events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { panic("middleware blew up") }
	}
	var trace string
	err := wrapped(t, tracingSub(&trace, "x.Y", "C1", "g"), blowUp)(context.Background(), &events.Message{Event: "x.Y"})

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

// One panic yields exactly one *PanicError, whichever recover catches it.
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
			err := wrapped(t, c.sub, c.chain...)(context.Background(), &events.Message{Event: "x.Y"})

			var panicked *events.PanicError
			if !errors.As(err, &panicked) {
				t.Fatalf("got %v, want a *PanicError", err)
			}
			// A re-wrapped PanicError would be the outer one's Value.
			if inner, ok := panicked.Value.(*events.PanicError); ok {
				t.Errorf("two PanicErrors for one panic; the outer recover wrapped %v", inner)
			}
			if panicked.Value != c.wantValue {
				t.Errorf("PanicError.Value = %v, want %v", panicked.Value, c.wantValue)
			}
		})
	}
}

// passthrough is a middleware that only delegates.
func passthrough() events.Middleware {
	return func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error { return next(ctx, msg) }
	}
}

// panicking is a middleware that panics without calling next.
func panicking(value any) events.Middleware {
	return func(events.Subscription, events.Handler) events.Handler {
		return func(context.Context, *events.Message) error { panic(value) }
	}
}

// A real delivery goroutine survives a panicking chain over a panicking handler.
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
	start(t, context.Background(), bus, panickingSub("from the handler"))
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
	// The chain panics before the handler runs.
	if panicked.Value != "from the chain" {
		t.Errorf("PanicError.Value = %v, want the chain's panic", panicked.Value)
	}
}

// A chain folded by Apply sees a handler's panic only with Recover at its innermost end.
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
			start(t, context.Background(), bus, subs...)

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
