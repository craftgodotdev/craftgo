package matrix

import (
	"context"
	"strings"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// dispositionProbe is a listener that records whether its logic ran, so a
// delivery the descriptor's wrapper turned back can be told from one it
// dispatched.
type dispositionProbe struct{ ran int }

func (p *dispositionProbe) MirrorStock(context.Context, *eventtypes.ItemStocked) error {
	p.ran++
	return nil
}

// notifyProbe stands in for the notification module's logic on the one
// contract this file exercises.
type notifyProbe struct{ ran int }

func (p *notifyProbe) NotifyDispatch(context.Context, *eventtypes.ShipmentDispatched) error {
	p.ran++
	return nil
}

// deliver runs one registered subscription the way a transport does. The
// bus is the same one the message was encoded through, and the handler
// called is the one the TRANSPORT is handed - not the descriptor's own,
// which is a frame short of what arrives: reaching the handler is marked
// between the two, so a test that called the inner one would read false
// on a delivery that dispatched.
func deliver(t *testing.T, subscribe func(*craftevents.Bus) error, publish func(*craftevents.Bus) error) (*craftevents.Message, error) {
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
	if err := subscribe(bus); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(tr.subs) != 1 {
		t.Fatalf("the transport was handed %d subscription(s), want 1", len(tr.subs))
	}
	return msg, tr.subs[0].Handle(context.Background(), msg)
}

// The descriptor's wrapper decodes, validates, then dispatches - and
// decides nothing about the delivery on the chain's behalf. Both halves
// matter.
//
// Validating first is what keeps a payload that cannot be decoded or
// cannot satisfy its constraints out of logic that assumes both. Deciding
// nothing is what leaves the choice where the application puts it: a
// disposition is a middleware's to write, and a wrapper that settled or
// rejected on its own would overrule every chain above it with nothing to
// see it happen - the transport reads the last decision, not the reason.
//
// Nothing here is a state the broker can be asked about afterwards: an
// accepted record and a rejected one are both terminal and both advance
// the share-group offset. The reading has to be taken off the delivery,
// before an adapter answers for it.
func TestTheDescriptorValidatesBeforeDispatchAndDecidesNothing(t *testing.T) {
	t.Run("a valid payload dispatches once and is left undecided", func(t *testing.T) {
		probe := &dispositionProbe{}
		delivered, err := deliver(t,
			func(bus *craftevents.Bus) error {
				return events.ItemStocked.Subscribe(bus, consumers.InventoryGroup, probe.MirrorStock)
			},
			func(bus *craftevents.Bus) error {
				return events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
					InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
					Quantity:        1,
				})
			})
		if err != nil {
			t.Fatalf("the wrapper turned a valid payload back: %v", err)
		}
		if probe.ran != 1 {
			t.Fatalf("the handler ran %d times, want 1", probe.ran)
		}
		if got := delivered.Disposition(); got != craftevents.DispositionUnset {
			t.Errorf("nothing decided anything, yet the delivery carries %v - a wrapper that decides for the chain takes the choice away from it", got)
		}
	})

	t.Run("a payload the wrapper turns back never reaches logic and is left undecided", func(t *testing.T) {
		// carrier is @minLength(1), so an empty one fails Validate and
		// never reaches NotifyDispatch. The descriptor's Publish would
		// refuse it, so it goes on the bus untyped - the way a message
		// from another system arrives.
		probe := &notifyProbe{}
		delivered, err := deliver(t,
			func(bus *craftevents.Bus) error {
				return events.ShipmentDispatched.Subscribe(bus, consumers.NotificationGroup, probe.NotifyDispatch)
			},
			func(bus *craftevents.Bus) error {
				return bus.Publish(context.Background(), events.ShipmentDispatchedContract,
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
		if got := delivered.Disposition(); got != craftevents.DispositionUnset {
			t.Errorf("the wrapper decided %v on the chain's behalf; the decision is the chain's to make", got)
		}
	})
}
