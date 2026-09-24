package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/events"
)

// tagMW returns a middleware that appends ">tag" to trace before delegating and "<tag"
// after.
func tagMW(trace *string, tag string) events.Middleware {
	return func(_ events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			*trace += ">" + tag
			err := next(ctx, msg)
			*trace += "<" + tag
			return err
		}
	}
}

// tracingSub builds one subscription whose handler marks the trace.
func tracingSub(trace *string, event, consumer string, group events.Group) events.Subscription {
	return events.Subscription{
		Event: event, Consumer: consumer, Group: group,
		Handle: func(context.Context, *events.Message) error {
			*trace += "|H|"
			return nil
		},
	}
}

// deliver runs one subscription's handler the way a transport would.
func deliver(t *testing.T, sub events.Subscription) error {
	t.Helper()
	return sub.Handle(context.Background(), &events.Message{Event: sub.Event})
}

// A chain folds outermost-first: the first middleware listed is the first a message
// enters.
func TestChainApplyFoldsOutermostFirst(t *testing.T) {
	var trace string
	out := events.NewChain(tagMW(&trace, "A"), tagMW(&trace, "B"), tagMW(&trace, "C")).
		Apply([]events.Subscription{tracingSub(&trace, "x.Y", "C1", "g")})
	if err := deliver(t, out[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	want := ">A>B>C|H|<C<B<A"
	if trace != want {
		t.Errorf("chain order = %q, want %q (outermost-first)", trace, want)
	}
}

// A forward fold makes the last middleware outermost, so it traces differently from
// Apply.
func TestForwardFoldWouldReverseTheOrder(t *testing.T) {
	var forward string
	sub := tracingSub(&forward, "x.Y", "C1", "g")
	h := sub.Handle
	for _, mw := range []events.Middleware{tagMW(&forward, "A"), tagMW(&forward, "B"), tagMW(&forward, "C")} {
		h = mw(sub, h)
	}
	if err := h(context.Background(), &events.Message{Event: "x.Y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	var applied string
	out := events.NewChain(tagMW(&applied, "A"), tagMW(&applied, "B"), tagMW(&applied, "C")).
		Apply([]events.Subscription{tracingSub(&applied, "x.Y", "C1", "g")})
	if err := deliver(t, out[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if forward == applied {
		t.Fatalf("Apply folds forwards: both orders traced %q", applied)
	}
	if want := ">C>B>A|H|<A<B<C"; forward != want {
		t.Errorf("forward fold = %q, want %q", forward, want)
	}
}

// A middleware is handed the subscription it wraps, so one chain reads a
// different Event, Consumer and Group per consumer.
func TestMiddlewareSeesItsOwnSubscription(t *testing.T) {
	var seen []string
	record := func(sub events.Subscription, next events.Handler) events.Handler {
		return func(ctx context.Context, msg *events.Message) error {
			seen = append(seen, sub.Event+"/"+sub.Consumer+"/"+string(sub.Group))
			return next(ctx, msg)
		}
	}
	var trace string
	subs := []events.Subscription{
		tracingSub(&trace, "orders.Placed", "SendReceipt", "receipts"),
		tracingSub(&trace, "orders.Placed", "Audit", "audit"),
		tracingSub(&trace, "stock.Low", "Reorder", "warehouse"),
	}
	for _, sub := range events.NewChain(record).Apply(subs) {
		if err := deliver(t, sub); err != nil {
			t.Fatalf("deliver %s: %v", sub.Consumer, err)
		}
	}

	want := []string{
		"orders.Placed/SendReceipt/receipts",
		"orders.Placed/Audit/audit",
		"stock.Low/Reorder/warehouse",
	}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("middleware saw %v, want %v", seen, want)
	}
}

// A nil Chain returns the caller's slice itself, handlers unchanged.
func TestNilChainApplyReturnsTheSliceUnchanged(t *testing.T) {
	var trace string
	subs := []events.Subscription{tracingSub(&trace, "x.Y", "C1", "g")}
	var none events.Chain
	out := none.Apply(subs)
	if len(out) != 1 || &out[0] != &subs[0] {
		t.Fatal("nil chain copied the slice; want the caller's own")
	}
	if err := deliver(t, out[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if trace != "|H|" {
		t.Errorf("nil chain changed behaviour: trace %q, want %q", trace, "|H|")
	}
}

func TestChainApplySkipsNil(t *testing.T) {
	var trace string
	out := events.NewChain(tagMW(&trace, "A"), nil, tagMW(&trace, "C")).
		Apply([]events.Subscription{tracingSub(&trace, "x.Y", "C1", "g")})
	if err := deliver(t, out[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if strings.Contains(trace, "B") || !strings.Contains(trace, ">A") || !strings.Contains(trace, ">C") {
		t.Errorf("nil middleware should be skipped silently; got trace %q", trace)
	}
}

// Apply leaves the caller's subscriptions undecorated.
func TestChainApplyDoesNotMutateItsInput(t *testing.T) {
	var trace string
	subs := []events.Subscription{tracingSub(&trace, "x.Y", "C1", "g")}
	events.NewChain(tagMW(&trace, "A")).Apply(subs)
	if err := deliver(t, subs[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if trace != "|H|" {
		t.Errorf("input subscription was decorated in place; trace %q", trace)
	}
}

// Append leaves its receiver unchanged.
func TestChainAppendDoesNotMutateReceiver(t *testing.T) {
	var trace string
	base := events.NewChain(tagMW(&trace, "A"))
	derived := base.Append(tagMW(&trace, "B"))

	out := base.Apply([]events.Subscription{tracingSub(&trace, "x.Y", "C1", "g")})
	if err := deliver(t, out[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if strings.Contains(trace, "B") {
		t.Errorf("base chain leaked Append target; trace %q must not contain B", trace)
	}

	trace = ""
	out = derived.Apply([]events.Subscription{tracingSub(&trace, "x.Y", "C1", "g")})
	if err := deliver(t, out[0]); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if want := ">A>B|H|<B<A"; trace != want {
		t.Errorf("derived chain = %q, want %q", trace, want)
	}
}
