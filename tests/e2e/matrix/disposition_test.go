package matrix

import (
	"context"
	"strings"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	inventoryevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/inventory_service"
	notificationevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/notification_service"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// dispositionProbe is a handler set that records whether its logic ran,
// standing in for the generated one so a delivery the wrapper turned back
// can be told from one it dispatched.
type dispositionProbe struct{ ran int }

func (p *dispositionProbe) MirrorStock(context.Context, *eventtypes.ItemStocked) error {
	p.ran++
	return nil
}

// notifyProbe is the notification service's handler set. Only
// NotifyDispatch is exercised; the rest satisfy the interface.
type notifyProbe struct{ ran int }

func (p *notifyProbe) SendStockAlert(context.Context, *eventtypes.ItemStocked) error      { return nil }
func (p *notifyProbe) AuditReconciliation(context.Context, *eventtypes.ItemStocked) error { return nil }
func (p *notifyProbe) TrackStocktake(context.Context, *eventtypes.StocktakeStarted) error { return nil }
func (p *notifyProbe) NotifyDispatch(context.Context, *eventtypes.ShipmentDispatched) error {
	p.ran++
	return nil
}

// deliver runs one generated subscription the way a transport does. The
// bus is the same one the publisher encoded through, and the handler
// called is the one the TRANSPORT is handed - not the subscription's own,
// which is a frame short of what arrives: reaching the consumer is marked
// between the two, so a test that called the inner one would read false
// on a delivery that dispatched.
func deliver(t *testing.T, build func(*craftevents.Bus) []craftevents.Subscription, consumer string, publish func(*craftevents.Bus) error) (*craftevents.Message, error) {
	t.Helper()
	tr := &handingTransport{}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	if err := publish(bus); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msg := tr.msg
	if msg == nil {
		t.Fatal("the transport was handed no message")
	}
	for _, sub := range build(bus) {
		if sub.Consumer != consumer {
			continue
		}
		if err := bus.Subscribe(context.Background(), sub); err != nil {
			t.Fatalf("subscribe %s: %v", consumer, err)
		}
		return msg, tr.sub.Handle(context.Background(), msg)
	}
	t.Fatalf("the design no longer declares a %s consumer", consumer)
	return nil, nil
}

// A middleware reads two things off a delivery to decide what to ask the
// broker for: whether the chain REACHED the consumer's own logic, and
// what - if anything - anyone decided. Both are written beneath the
// generated wrapper, and the wrapper is the one frame a transport test
// cannot reach.
//
// The pair is what separates "the logic failed" from "the payload never
// got that far", and a chain that could not tell them apart would do the
// opposite of what it meant on the case that matters: a poison payload
// that can never validate would be handed back forever, while a
// transient handler failure would be settled and lost.
//
// Neither is a state the broker can be asked about afterwards - an
// accepted record and a rejected one are both terminal and both advance
// the share-group offset - so the reading has to be taken here, off the
// delivery, before an adapter answers for it.
//
// What the pair can and cannot separate is the point: both cases below
// report the same Reached, because a generated subscription's handler is
// the wrapper rather than the logic inside it.
func TestTheGeneratedWrapperReportsWhatTheChainReached(t *testing.T) {
	t.Run("a payload that dispatches is reached and left undecided", func(t *testing.T) {
		probe := &dispositionProbe{}
		delivered, err := deliver(t,
			func(bus *craftevents.Bus) []craftevents.Subscription {
				return inventoryevents.Subscriptions(bus, probe)
			}, "MirrorStock",
			func(bus *craftevents.Bus) error {
				return inventoryevents.NewPublisher(bus).PublishItemStocked(context.Background(),
					&eventtypes.ItemStocked{
						InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
						Quantity:        1,
					})
			})
		if err != nil {
			t.Fatalf("the wrapper turned a valid payload back: %v", err)
		}
		if probe.ran != 1 {
			t.Fatalf("the consumer ran %d times, want 1", probe.ran)
		}
		if !delivered.Reached() {
			t.Error("the delivery reports it never entered the consumer, but the consumer ran")
		}
		if got := delivered.Disposition(); got != craftevents.DispositionUnset {
			t.Errorf("nothing decided anything, yet the delivery carries %v - a wrapper that decides for the chain takes the choice away from it", got)
		}
	})

	t.Run("a payload the wrapper turns back never reaches logic and stays undecided", func(t *testing.T) {
		// carrier is @minLength(1), so an empty one fails Validate and
		// never reaches NotifyDispatch.
		probe := &notifyProbe{}
		delivered, err := deliver(t,
			func(bus *craftevents.Bus) []craftevents.Subscription {
				return notificationevents.Subscriptions(bus, probe)
			}, "NotifyDispatch",
			func(bus *craftevents.Bus) error {
				return inventoryevents.NewPublisher(bus).PublishShipmentDispatched(context.Background(),
					&eventtypes.ShipmentDispatched{ShipmentID: "shp-2"})
			})
		if err == nil {
			t.Fatal("an invalid payload came back without an error")
		}
		if !strings.Contains(err.Error(), "events.ShipmentDispatched") {
			t.Errorf("the failure does not name the contract it arrived on: %v", err)
		}
		if probe.ran != 0 {
			t.Errorf("an invalid payload reached logic %d time(s)", probe.ran)
		}
		// Reached is marked on the way INTO the subscription's handler,
		// and on a generated subscription that handler is the wrapper -
		// decode, validate, dispatch. So a payload the wrapper turned
		// back reads the same as one the logic ran and failed, and the
		// returned error is the only thing separating them. Pinned as it
		// behaves, not as a chain writing "retry what reached the logic"
		// would want it: that chain hands a payload that can never
		// validate back forever, and nothing here would say so.
		if !delivered.Reached() {
			t.Error("Reached is false on a delivery that entered the wrapper - the mark moved, and a chain reading it now means something else")
		}
		if got := delivered.Disposition(); got != craftevents.DispositionUnset {
			t.Errorf("the wrapper decided %v on the chain's behalf; the decision is the chain's to make", got)
		}
	})
}
