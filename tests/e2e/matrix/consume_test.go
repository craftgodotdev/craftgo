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

	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/xshared"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// tracer collects one entry per middleware frame. The consumers of a
// contract run on one goroutine per group, so the trace is shared state.
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

// promoteTier publishes the one contract with a single consumer, so the
// trace it produces is one goroutine's and ordering is unambiguous.
func promoteTier(t *testing.T, svc *svccontext.ServiceContext) {
	t.Helper()
	promoteMember(t, svc, "m-1")
}

// promoteMember is promoteTier for a chosen member id. "panic-please" is
// the one the TrackTier logic stub panics on.
func promoteMember(t *testing.T, svc *svccontext.ServiceContext, member string) {
	t.Helper()
	if err := svc.Events.InventoryService.PublishTierPromoted(context.Background(), &eventtypes.TierPromoted{
		Tier: xshared.XTierGold, MemberID: member,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// The chain wraps every subscription the generated SubscribeAll builds,
// outermost first: the first middleware listed is the first frame a
// message enters.
func TestConsumerChainWrapsGeneratedSubscriptionsOutermostFirst(t *testing.T) {
	tr := &tracer{}
	chain := craftevents.NewChain(tr.tag("A"), tr.tag("B"), tr.tag("C"))
	svc, transport := bootEventsWith(t, chain, nil)

	promoteTier(t, svc)
	transport.Drain()

	if got := svc.DeliveredTo("TrackTier"); len(got) != 1 {
		t.Fatalf("TrackTier received %d payloads, want 1", len(got))
	}
	want := ">A>B>C<C<B<A"
	if got := strings.Join(tr.seen(), ""); got != want {
		t.Errorf("chain order = %q, want %q (outermost-first)", got, want)
	}
}

// The consumer chain and the HTTP chain fold the same way. Two
// conventions in one framework is a papercut a reader pays for forever,
// so the orders are traced side by side rather than asserted apart.
func TestConsumerChainFoldsLikeTheHTTPChain(t *testing.T) {
	consumerTrace := &tracer{}
	svc, transport := bootEventsWith(t,
		craftevents.NewChain(consumerTrace.tag("A"), consumerTrace.tag("B"), consumerTrace.tag("C")), nil)
	promoteTier(t, svc)
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

// Every middleware is handed the subscription it wraps, so one chain
// reads a different contract, consumer and group per subscription. That
// is why the event side needs no decorator to select a target: what HTTP
// names in the DSL arrives here as an argument.
func TestMiddlewareSeesEachSubscriptionsIdentity(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	record := func(sub craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			mu.Lock()
			seen = append(seen, sub.Event+" "+sub.Consumer+" "+sub.GroupName())
			mu.Unlock()
			return next(ctx, msg)
		}
	}
	svc, transport := bootEventsWith(t, craftevents.NewChain(record), nil)

	// ItemStocked is consumed four times: by its declaring service, by a
	// consumer in another package, by one that pins its own group, and by
	// one carrying a declared consume-middleware chain.
	if err := svc.Events.InventoryService.PublishItemStocked(context.Background(), &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        3,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	got := seen
	sort.Strings(got)
	want := []string{
		"events.ItemStocked CountStocked analytics-worker",
		"events.ItemStocked GuardedStock eventsubs-GuardedService-GuardedStock",
		"events.ItemStocked MirrorStock events-InventoryService-MirrorStock",
		"events.ItemStocked SendStockAlert eventsubs-NotificationService-SendStockAlert",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("middleware saw\n%v\nwant\n%v", got, want)
	}
}

// A bus with no middleware registers exactly what the generated
// SubscribeAll built - every other event test in this fixture boots that
// way and still passes.
func TestNoMiddlewareLeavesDeliveryUnchanged(t *testing.T) {
	svc, transport := bootEventsWith(t, nil, nil)
	promoteTier(t, svc)
	transport.Drain()

	if got := svc.DeliveredTo("TrackTier"); len(got) != 1 {
		t.Errorf("TrackTier received %d payloads with no middleware, want 1", len(got))
	}
}

// A panicking consumer reaches the project's own middleware as an error
// and the delivery goroutine survives - with nothing to add to the chain.
// The panic fires inside the generated consumer on a goroutine the memory
// transport spawned, which is the only honest place to observe this.
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
	svc, transport := bootEventsWith(t, craftevents.NewChain(observe),
		func(_ craftevents.Subscription, err error) {
			mu.Lock()
			reported = err
			mu.Unlock()
		})
	promoteMember(t, svc, "panic-please")
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	var panicked *craftevents.PanicError
	if !errors.As(seenByMiddleware, &panicked) {
		t.Fatalf("middleware saw %v, want a *PanicError", seenByMiddleware)
	}
	if panicked.Consumer != "TrackTier" || panicked.Group != "analytics-tier-worker" {
		t.Errorf("PanicError names %s/%s", panicked.Consumer, panicked.Group)
	}
	if !errors.As(reported, &panicked) {
		t.Errorf("the transport was told %v, want the same *PanicError", reported)
	}
}

// A panicking MIDDLEWARE sits above the inner recover, so the project's
// own middleware below it does not see the panic - but the outer recover
// still keeps the delivery goroutine alive.
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
	svc, transport := bootEventsWith(t, craftevents.NewChain(observe, boom),
		func(_ craftevents.Subscription, err error) {
			mu.Lock()
			reported = err
			mu.Unlock()
		})
	promoteTier(t, svc)
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
