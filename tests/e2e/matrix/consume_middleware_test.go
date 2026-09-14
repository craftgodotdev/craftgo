package matrix

import (
	"context"
	"errors"
	"strings"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"

	guardedevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/guarded_service"
	apptransport "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/transport"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// redeliverTransport is a broker that can hand a message back: it
// re-delivers while the chain asks for it, counting deliveries the way an
// adapter does through SetDeliveries.
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

func (t *redeliverTransport) Subscribe(ctx context.Context, sub craftevents.Subscription) error {
	if t.msg == nil {
		return errors.New("nothing was published to deliver")
	}
	for n := 1; n <= t.max; n++ {
		t.msg.SetDeliveries(n)
		t.msg.Settle()
		_ = sub.Handle(ctx, t.msg)
		if t.msg.Disposition() != craftevents.DispositionRedeliver {
			return nil
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

// failingConsumers fails GuardedStock every time and counts the runs.
type failingConsumers struct{ ran int }

func (c *failingConsumers) GuardedStock(context.Context, *eventtypes.ItemStocked) error {
	c.ran++
	return errors.New("boom")
}
func (c *failingConsumers) BareStock(context.Context, *eventtypes.StocktakeStarted) error {
	return nil
}
func (c *failingConsumers) InheritedStock(context.Context, *eventtypes.WarehouseClosed) error {
	return nil
}

// The design writes `@consumeMiddlewares(Settle)` on the service and
// appends `@consumeMiddlewares(Attempt)` on the consumer, so Settle is
// outermost and Attempt sits nearest the handler. That is the ONLY order
// in which the retry works: Settle answers nil, so an Attempt above it is
// handed a success and never reaches its Redeliver call.
//
// The assertion counts attempts rather than reading frames on purpose. A
// reversed chain still ENTERS in the order it was written, so a
// frame-order assertion passes on the broken one and this does not.
func TestGeneratedConsumeChainRetriesBeforeItParks(t *testing.T) {
	var asked, parked int
	tr := &redeliverTransport{max: 10}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	if err := bus.Publish(context.Background(), "events.ItemStocked", &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"}, Quantity: 1}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	h := &failingConsumers{}
	mw := guardedevents.Middlewares{Settle: settle(&parked), Attempt: attempt(3, &asked)}
	for _, sub := range guardedevents.Subscriptions(bus, h, mw) {
		if sub.Consumer != "GuardedStock" {
			continue
		}
		if err := bus.Subscribe(context.Background(), sub); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
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
func TestReversedConsumeChainSilentlyStopsRetrying(t *testing.T) {
	var asked, parked int
	tr := &redeliverTransport{max: 10}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	if err := bus.Publish(context.Background(), "events.ItemStocked", &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"}, Quantity: 1}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	h := &failingConsumers{}
	reversed := craftevents.NewChain(attempt(3, &asked), settle(&parked), craftevents.Recover())
	core := guardedevents.Subscriptions(bus, h, guardedevents.Middlewares{})
	for _, sub := range core {
		if sub.Consumer != "GuardedStock" {
			continue
		}
		if err := bus.Subscribe(context.Background(), reversed.Apply([]craftevents.Subscription{sub})[0]); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	if h.ran != 1 || asked != 0 {
		t.Errorf("reversed chain ran %d time(s) and asked %d retr(ies); want 1 and 0", h.ran, asked)
	}
	if parked != 1 {
		t.Errorf("the reversed chain still parks the message: parked %d, want 1", parked)
	}
}

// @ignoreMiddleware clears the inherited chain, so BareStock is emitted
// with no wrap at all - the service-level Settle does not reach it.
func TestIgnoreMiddlewareLeavesTheConsumerBare(t *testing.T) {
	var parked, asked int
	bus := craftevents.New(craftevents.WithTransport(&redeliverTransport{max: 1}), craftevents.WithCodec(codecjson.Codec{}))
	mw := guardedevents.Middlewares{Settle: settle(&parked), Attempt: attempt(3, &asked)}
	for _, sub := range guardedevents.Subscriptions(bus, &failingConsumers{}, mw) {
		if sub.Consumer != "BareStock" {
			continue
		}
		msg := &craftevents.Message{Event: "events.StocktakeStarted", Payload: []byte(`{"warehouse":1}`)}
		if err := sub.Handle(context.Background(), msg); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	if parked != 0 || asked != 0 {
		t.Errorf("an @ignoreMiddleware consumer ran the inherited chain: parked=%d asked=%d", parked, asked)
	}
}

// SubscribeAll refuses a container missing a consume middleware the
// design applies, and it is SubscribeAll rather than wiring.Register that
// carries the check: a consumer deployable owns no *server.Server, so it
// calls this directly and never reaches Register at all. Guarding only
// Register would cover the deployables that serve HTTP and miss exactly
// the ones this feature is for.
func TestSubscribeAllRefusesAnUnwiredConsumeMiddleware(t *testing.T) {
	bus := craftevents.New(
		craftevents.WithTransport(memory.New()),
		craftevents.WithCodec(codecjson.Codec{}))
	svc := svccontext.NewServiceContext()
	svc.Events = svccontext.NewEvents(bus)
	// Settle is wired, Attempt is not - the shape of forgetting one line.
	svc.Events.Consume.Settle = func(_ craftevents.Subscription, next craftevents.Handler) craftevents.Handler {
		return next
	}

	err := apptransport.SubscribeAll(context.Background(), bus, svc)
	if err == nil {
		t.Fatal("subscribed with a declared consume middleware left nil")
	}
	for _, want := range []string{
		"consume middleware Attempt",
		"GuardedService.GuardedStock runs it",
		"svcCtx.Events.Consume.Attempt is nil",
		"svc.Events.Consume.Attempt = consume.NewAttemptMiddleware",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
}

// The complement: a fully wired container subscribes.
func TestSubscribeAllAcceptsAWiredContainer(t *testing.T) {
	bus := craftevents.New(
		craftevents.WithTransport(memory.New()),
		craftevents.WithCodec(codecjson.Codec{}))
	svc := svccontext.NewServiceContext()
	svc.Events = svccontext.NewEvents(bus)
	wireConsumeMiddleware(&svc.Events.Consume)
	if err := apptransport.SubscribeAll(context.Background(), bus, svc); err != nil {
		t.Fatalf("a wired container was refused: %v", err)
	}
}
