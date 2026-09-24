package matrix

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/server"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/xshared"
)

// tracer records middleware frames from concurrent deliveries.
type tracer struct {
	mu    sync.Mutex
	marks []string
}

func (tr *tracer) mark(s string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.marks = append(tr.marks, s)
}

func (tr *tracer) seen() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]string, len(tr.marks))
	copy(out, tr.marks)
	return out
}

// tag returns a middleware marking the trace either side of next.
func (tr *tracer) tag(name string) craftevents.Middleware {
	return func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			tr.mark(">" + name)
			err := next(ctx, msg)
			tr.mark("<" + name)
			return err
		}
	}
}

// promoteTier publishes events.TierPromoted, whose single listener keeps
// the trace on one goroutine.
func promoteTier(t *testing.T, bus *craftevents.Bus) {
	t.Helper()
	promoteMember(t, bus, "m-1")
}

// promoteMember is promoteTier for a chosen member; TrackTier panics on
// "panic-please".
func promoteMember(t *testing.T, bus *craftevents.Bus, member string) {
	t.Helper()
	if err := events.TierPromoted.Publish(context.Background(), bus, &eventtypes.TierPromoted{
		Tier: xshared.XTierGold, MemberID: member,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// The first middleware of a bus chain is the outermost frame.
func TestConsumerChainWrapsEverySubscriptionOutermostFirst(t *testing.T) {
	tr := &tracer{}
	chain := craftevents.NewChain(tr.tag("A"), tr.tag("B"), tr.tag("C"))
	svc, bus, transport := bootEventsWith(t, chain, nil)

	promoteTier(t, bus)
	transport.Drain()

	if got := svc.DeliveredTo("TrackTier"); len(got) != 1 {
		t.Fatalf("TrackTier received %d payloads, want 1", len(got))
	}
	want := ">A>B>C<C<B<A"
	if got := strings.Join(tr.seen(), ""); got != want {
		t.Errorf("chain order = %q, want %q (outermost-first)", got, want)
	}
}

// A consumer chain and an HTTP chain nest the same middleware list alike.
func TestConsumerChainFoldsLikeTheHTTPChain(t *testing.T) {
	consumerTrace := &tracer{}
	_, bus, transport := bootEventsWith(t,
		craftevents.NewChain(consumerTrace.tag("A"), consumerTrace.tag("B"), consumerTrace.tag("C")), nil)
	promoteTier(t, bus)
	transport.Drain()

	httpTrace := &tracer{}
	httpTag := func(name string) server.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpTrace.mark(">" + name)
				next.ServeHTTP(w, r)
				httpTrace.mark("<" + name)
			})
		}
	}
	server.NewChain(httpTag("A"), httpTag("B"), httpTag("C")).
		Then(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	got, want := strings.Join(consumerTrace.seen(), ""), strings.Join(httpTrace.seen(), "")
	if got != want {
		t.Errorf("consumer chain folded %q, HTTP chain folded %q - the two must agree", got, want)
	}
}

// A bus middleware is handed each subscription's contract, consumer and group.
func TestMiddlewareSeesEachSubscriptionsIdentity(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	record := func(sub craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			mu.Lock()
			seen = append(seen, sub.Event+" "+sub.Consumer+" "+string(sub.Group))
			mu.Unlock()
			return next(ctx, msg)
		}
	}
	_, bus, transport := bootEventsWith(t, craftevents.NewChain(record), nil)

	// Four listeners, one group each; Consumer defaults to the contract.
	if err := events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        3,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	got := seen
	sort.Strings(got)
	want := []string{
		"events.ItemStocked events.ItemStocked analytics-worker",
		"events.ItemStocked events.ItemStocked matrix-guarded",
		"events.ItemStocked events.ItemStocked matrix-inventory",
		"events.ItemStocked events.ItemStocked matrix-notifications",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("middleware saw\n%v\nwant\n%v", got, want)
	}
}

// A bus with no middleware delivers to the registered handler.
func TestNoMiddlewareLeavesDeliveryUnchanged(t *testing.T) {
	svc, bus, transport := bootEventsWith(t, nil, nil)
	promoteTier(t, bus)
	transport.Drain()

	if got := svc.DeliveredTo("TrackTier"); len(got) != 1 {
		t.Errorf("TrackTier received %d payloads with no middleware, want 1", len(got))
	}
}

// A handler panic reaches the project's middleware and the transport as a
// *PanicError naming the subscription.
func TestPanicInLogicReachesTheProjectsMiddleware(t *testing.T) {
	var mu sync.Mutex
	var seenByMiddleware, reported error
	observe := func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			err := next(ctx, msg)
			mu.Lock()
			seenByMiddleware = err
			mu.Unlock()
			return err
		}
	}
	_, bus, transport := bootEventsWith(t, craftevents.NewChain(observe),
		func(_ craftevents.Subscription, err error) {
			mu.Lock()
			reported = err
			mu.Unlock()
		})
	promoteMember(t, bus, "panic-please")
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	var panicked *craftevents.PanicError
	if !errors.As(seenByMiddleware, &panicked) {
		t.Fatalf("middleware saw %v, want a *PanicError", seenByMiddleware)
	}
	if panicked.Event != events.TierPromotedContract || panicked.Group != "analytics-tier-worker" {
		t.Errorf("PanicError names %s/%s", panicked.Event, panicked.Group)
	}
	if !errors.As(reported, &panicked) {
		t.Errorf("the transport was told %v, want the same *PanicError", reported)
	}
}

// A middleware panic escapes the inner recover: the middleware wrapping it
// sees no error, and the outer recover reports a *PanicError.
func TestPanicInTheChainIsCaughtButNotObservable(t *testing.T) {
	var mu sync.Mutex
	var seenByMiddleware, reported error
	observe := func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			err := next(ctx, msg)
			mu.Lock()
			seenByMiddleware = err
			mu.Unlock()
			return err
		}
	}
	boom := func(craftevents.Subscription, craftevents.Handler) craftevents.Handler {
		return func(context.Context, *craftevents.Message) error { panic("middleware blew up") }
	}
	_, bus, transport := bootEventsWith(t, craftevents.NewChain(observe, boom),
		func(_ craftevents.Subscription, err error) {
			mu.Lock()
			reported = err
			mu.Unlock()
		})
	promoteTier(t, bus)
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	var panicked *craftevents.PanicError
	if !errors.As(reported, &panicked) {
		t.Fatalf("the transport was told %v, want a *PanicError", reported)
	}
	if panicked.Value != "middleware blew up" {
		t.Errorf("PanicError.Value = %v, want the middleware's panic", panicked.Value)
	}
	if seenByMiddleware != nil {
		t.Errorf("the middleware below the panic saw %v, want nothing", seenByMiddleware)
	}
}
