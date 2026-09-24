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
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// redeliverTransport delivers the published message, up to max times while
// the chain asks for it back.
type redeliverTransport struct {
	msg *craftevents.Message
	max int
}

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

// settle is a dead-letter middleware: it parks a failure that is not being
// redelivered and reports success.
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

// attempt is a retry middleware: it redelivers a failure while Deliveries is
// under budget, then rejects it, returning the error either way.
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

// subscribeGuarded registers the guarded block of consumers.Register.
func subscribeGuarded(bus *craftevents.Bus, h guardedLogic) error {
	return errors.Join(
		events.ItemStocked.Subscribe(bus, consumers.GuardedGroup, h.GuardedStock),
		events.StocktakeStarted.Subscribe(bus, consumers.GuardedGroup, h.BareStock),
		events.WarehouseClosed.Subscribe(bus, consumers.GuardedGroup, h.InheritedStock),
	)
}

// guardedLogic is the logic subscribeGuarded dispatches to.
type guardedLogic interface {
	GuardedStock(context.Context, *eventtypes.ItemStocked) error
	BareStock(context.Context, *eventtypes.StocktakeStarted) error
	InheritedStock(context.Context, *eventtypes.WarehouseClosed) error
}

// bootGuarded publishes one events.ItemStocked, then starts the guarded
// module on a redeliverTransport with chain installed bus-wide.
func bootGuarded(t *testing.T, chain craftevents.Chain, h guardedLogic) {
	t.Helper()
	tr := &redeliverTransport{max: 10}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	bus.Use(chain...)
	if err := events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := subscribeGuarded(bus, h); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
}

// With settle outermost, a failure is retried to budget before it is parked.
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

// With attempt outside settle, a failure is parked without a single retry.
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

// A bus with no chain neither retries nor parks a failure.
func TestNoChainLeavesTheHandlerBare(t *testing.T) {
	var asked, parked int
	h := &failingGuarded{}
	_, _ = settle(&parked), attempt(3, &asked)
	bootGuarded(t, nil, h)

	if h.ran != 1 {
		t.Errorf("an unchained handler ran %d time(s), want 1", h.ran)
	}
	if parked != 0 || asked != 0 {
		t.Errorf("an unchained bus ran a chain: parked=%d asked=%d", parked, asked)
	}
}

// A subscription's own Chain runs inside the bus-wide chain.
func TestASubscriptionsOwnChainRunsInsideTheBusChain(t *testing.T) {
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
	sub := events.ItemStocked.Subscription(bus, consumers.GuardedGroup, h.GuardedStock)
	sub.Chain = craftevents.NewChain(tr.tag("own"))
	if err := bus.Register(sub); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if ran != 1 {
		t.Fatalf("the handler ran %d time(s), want 1", ran)
	}
	if got, want := strings.Join(tr.seen(), ""), ">bus>own<own<bus"; got != want {
		t.Errorf("frames = %q, want %q - a subscription's own chain must run inside the bus chain", got, want)
	}
}

// countingGuarded counts GuardedStock runs and never fails.
type countingGuarded struct{ ran *int }

func (c *countingGuarded) GuardedStock(context.Context, *eventtypes.ItemStocked) error {
	*c.ran++
	return nil
}
func (c *countingGuarded) BareStock(context.Context, *eventtypes.StocktakeStarted) error { return nil }
func (c *countingGuarded) InheritedStock(context.Context, *eventtypes.WarehouseClosed) error {
	return nil
}
