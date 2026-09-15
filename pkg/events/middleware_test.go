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

// wrapped registers sub on a bus carrying mws and returns the handler the
// transport was handed - the one a delivery goroutine calls.
func wrapped(t *testing.T, sub events.Subscription, mws ...events.Middleware) events.Handler {
	t.Helper()
	bus, tr := busWith(mws...)
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

// Use builds the same chain WithMiddleware does, for a deployable that
// assembles its delivery chain after the bus rather than at the New call.
// The chain is folded at Start, so a middleware added after the
// registrations covers them too - every one of them, outside whatever
// chain a subscription carries itself.
func TestUseWrapsEverySubscriptionOutsideItsOwnChain(t *testing.T) {
	var trace string
	bus, tr := busWith(tagMW(&trace, "N"))
	sub := tracingSub(&trace, "x.Y", "C1", "g")
	sub.Chain = events.NewChain(tagMW(&trace, "S"))
	if err := bus.RegisterAll(sub, tracingSub(&trace, "other.Z", "C2", "g2")); err != nil {
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

// Use after Start is a wiring mistake and not a runtime condition: the
// batch is with the transport already wrapped, so a middleware arriving
// now would cover nothing and say nothing about it.
func TestUseAfterStartPanics(t *testing.T) {
	var trace string
	bus, _ := busWith()
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

// A subscription's own chain runs INSIDE the bus-wide one, so a bus
// concern - logging, tracing - still sees what a per-consumer chain did.
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

// The recover is outside both chains and inside neither: a subscription's
// own chain sees a panicking handler as an error, exactly as the bus
// chain does, and a panic in either chain still ends as an error.
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

// WithMiddleware covers a hand-built subscription too: one written
// against another design's contracts reaches the broker through the same
// bus, which is the only seam that sees every registration.
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
	bus, tr := busWith(record)
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

// A bus with no middleware is the wrap it has always applied: one recover
// around the handler, nothing else.
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
	err := wrapped(t, panickingSub("boom"), observe)(context.Background(), &events.Message{Event: "x.Y"})

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
			err := wrapped(t, c.sub, c.chain...)(context.Background(), &events.Message{Event: "x.Y"})

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
