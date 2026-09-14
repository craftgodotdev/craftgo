package matrix

import (
	"context"
	"errors"
	"strings"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/eventsubs"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// redeliverTransport is a broker that can hand a message back: it
// re-delivers while the chain asks for it, counting deliveries the way an
// adapter does through SetDeliveries. Only the subscriptions of the
// published contract are driven, so a handler set registered whole
// produces one delivery sequence.
type redeliverTransport struct {
	msg *craftevents.Message
	max int
}

// Publish keeps the encoded message so Subscribe can hand the same one
// back on each delivery, the way a broker replays what it stored.
func (t *redeliverTransport) Publish(_ context.Context, m *craftevents.Message) error {
	t.msg = m
	return nil
}
func (t *redeliverTransport) CanDisposition(craftevents.Disposition) bool { return true }

func (t *redeliverTransport) Subscribe(ctx context.Context, subs []craftevents.Subscription) error {
	if t.msg == nil {
		return errors.New("nothing was published to deliver")
	}
	for _, sub := range subs {
		if sub.Event != t.msg.Event {
			continue
		}
		for n := 1; n <= t.max; n++ {
			t.msg.SetDeliveries(n)
			t.msg.Settle()
			_ = sub.Handle(ctx, t.msg)
			if t.msg.Disposition() != craftevents.DispositionRedeliver {
				break
			}
		}
	}
	return nil
}

// settle parks a failure and reports success - what a dead-letter
// middleware does. It is the shape that makes chain order load-bearing.
func settle(parked *int) craftevents.Middleware {
	return func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			err := next(ctx, msg)
			if err == nil || msg.Disposition() == craftevents.DispositionRedeliver {
				return err
			}
			*parked++
			return nil
		}
	}
}

// attempt asks for the message back while its budget holds, returning the
// error either way - what a retry middleware does.
func attempt(budget int, asked *int) craftevents.Middleware {
	return func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return func(ctx context.Context, msg *craftevents.Message) error {
			err := next(ctx, msg)
			if err == nil {
				return nil
			}
			if msg.Deliveries() < budget {
				msg.Redeliver()
				*asked++
			} else {
				msg.Reject()
			}
			return err
		}
	}
}

// failingGuarded fails GuardedStock every time and counts the runs.
type failingGuarded struct{ ran int }

func (c *failingGuarded) GuardedStock(context.Context, *eventtypes.ItemStocked) error {
	c.ran++
	return errors.New("boom")
}
func (c *failingGuarded) BareStock(context.Context, *eventtypes.StocktakeStarted) error {
	return nil
}
func (c *failingGuarded) InheritedStock(context.Context, *eventtypes.WarehouseClosed) error {
	return nil
}

// bootGuarded registers GuardedService behind chain on a transport that
// can hand a message back, with one events.ItemStocked already published.
func bootGuarded(t *testing.T, chain craftevents.Chain, h eventsubs.GuardedServiceHandler) {
	t.Helper()
	tr := &redeliverTransport{max: 10}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	if err := events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := eventsubs.RegisterGuardedServiceHandler(bus, h, chain,
		eventsubs.GuardedServiceGroups{Default: consumers.GuardedGroup}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
}

// The chain is the application's, handed to Register once for the whole
// handler set: Settle is listed first so it is outermost, and Attempt
// sits nearest the handler. That is the ONLY order in which the retry
// works - Settle answers nil, so an Attempt above it is handed a success
// and never reaches its Redeliver call.
//
// The assertion counts attempts rather than reading frames on purpose. A
// reversed chain still ENTERS in the order it was written, so a
// frame-order assertion passes on the broken one and this does not.
func TestARegisteredChainRetriesBeforeItParks(t *testing.T) {
	var asked, parked int
	h := &failingGuarded{}
	bootGuarded(t, craftevents.NewChain(settle(&parked), attempt(3, &asked)), h)

	if h.ran != 3 {
		t.Errorf("handler ran %d time(s), want 3 - the chain is not retrying before it parks", h.ran)
	}
	if asked != 2 {
		t.Errorf("retry asked %d time(s), want 2", asked)
	}
	if parked != 1 {
		t.Errorf("dead-lettered %d time(s), want 1", parked)
	}
}

// The counter-case, and the reason the test above counts attempts: hand
// the same two middlewares over in the other order and the handler runs
// ONCE with zero retries, while the frame order still reads exactly as
// written and the message is still parked. Nothing about the shape of the
// run says it is broken.
func TestAReversedChainSilentlyStopsRetrying(t *testing.T) {
	var asked, parked int
	h := &failingGuarded{}
	bootGuarded(t, craftevents.NewChain(attempt(3, &asked), settle(&parked)), h)

	if h.ran != 1 || asked != 0 {
		t.Errorf("reversed chain ran %d time(s) and asked %d retr(ies); want 1 and 0", h.ran, asked)
	}
	if parked != 1 {
		t.Errorf("the reversed chain still parks the message: parked %d, want 1", parked)
	}
}

// Registering with no chain leaves the handler bare: a failure is neither
// retried nor parked, it just comes back to the transport. The chain is
// the application's to supply, and supplying none is a choice the design
// has no say in.
func TestNoChainLeavesTheHandlerBare(t *testing.T) {
	var asked, parked int
	h := &failingGuarded{}
	// The middlewares are built so the counters CAN move, and handed to
	// nothing, so a chain that crept in from anywhere else would show.
	_, _ = settle(&parked), attempt(3, &asked)
	bootGuarded(t, nil, h)

	if h.ran != 1 {
		t.Errorf("an unchained handler ran %d time(s), want 1", h.ran)
	}
	if parked != 0 || asked != 0 {
		t.Errorf("an unchained registration ran a chain: parked=%d asked=%d", parked, asked)
	}
}

// A subscription's own chain runs INSIDE the bus-wide one, so a bus-level
// concern still sees what a per-registration chain did. The two are
// wired at different places and both reach one delivery.
func TestTheRegisteredChainRunsInsideTheBusChain(t *testing.T) {
	tr := &tracer{}
	var ran int
	bus := craftevents.New(
		craftevents.WithTransport(&redeliverTransport{max: 1}),
		craftevents.WithCodec(codecjson.Codec{}),
		craftevents.WithMiddleware(tr.tag("bus")))
	if err := events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	h := &countingGuarded{ran: &ran}
	if err := eventsubs.RegisterGuardedServiceHandler(bus, h, craftevents.NewChain(tr.tag("own")),
		eventsubs.GuardedServiceGroups{Default: consumers.GuardedGroup}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if ran != 1 {
		t.Fatalf("the handler ran %d time(s), want 1", ran)
	}
	if got, want := strings.Join(tr.seen(), ""), ">bus>own<own<bus"; got != want {
		t.Errorf("frames = %q, want %q - the registered chain must run inside the bus chain", got, want)
	}
}

// countingGuarded is a handler set that only counts, so the frame order
// around it is the whole reading.
type countingGuarded struct{ ran *int }

func (c *countingGuarded) GuardedStock(context.Context, *eventtypes.ItemStocked) error {
	*c.ran++
	return nil
}
func (c *countingGuarded) BareStock(context.Context, *eventtypes.StocktakeStarted) error { return nil }
func (c *countingGuarded) InheritedStock(context.Context, *eventtypes.WarehouseClosed) error {
	return nil
}
