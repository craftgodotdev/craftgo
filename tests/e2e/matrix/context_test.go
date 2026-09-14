package matrix

import (
	"context"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// deliveryKey marks a value only the transport put on the context, so a
// handler reading it back proves the context it was called with is the
// one the delivery carried.
type deliveryKey struct{}

// handingTransport keeps the batch the bus registered and the message the
// bus encoded, then hands the one to the other on a context of its own.
// It stands in for what a broker adapter reaches a handler with - its own
// record, a header this application did not map, a trace it opened - none
// of which [craftevents.Message] carries.
type handingTransport struct {
	subs []craftevents.Subscription
	msg  *craftevents.Message
}

func (h *handingTransport) Subscribe(_ context.Context, subs []craftevents.Subscription) error {
	h.subs = subs
	return nil
}

func (h *handingTransport) Publish(_ context.Context, msg *craftevents.Message) error {
	h.msg = msg
	return nil
}

// contextProbe is a handler set satisfying the generated interface,
// standing in for the application's so the context a handler is called
// with can be read.
type contextProbe struct{ got context.Context }

func (p *contextProbe) MirrorStock(ctx context.Context, _ *eventtypes.ItemStocked) error {
	p.got = ctx
	return nil
}

// The context a handler is called with is the DELIVERY's, not one the
// wrapper made up. That is the whole reach a handler has to what
// [craftevents.Message] does not carry, and a wrapper that passed a fresh
// context instead would take it away with every handler still compiling
// and every payload still arriving.
//
// The subscription is built by the generated event descriptor, so what
// this pins is that descriptor's wrapper: decode, validate, dispatch, and
// the context going through all three untouched. That a Kafka delivery
// carries its record at all is the adapter's own business and is pinned
// there, so nothing here needs a broker.
func TestTheDeliveryContextReachesTheHandlerSet(t *testing.T) {
	tr := &handingTransport{}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))

	probe := &contextProbe{}
	if err := events.RegisterInventoryServiceHandler(bus, probe, nil,
		events.InventoryServiceGroups{Default: consumers.InventoryGroup}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}, craftevents.WithKey("sku-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if tr.msg == nil {
		t.Fatal("the transport was handed no message to deliver")
	}
	if len(tr.subs) != 1 {
		t.Fatalf("the transport was handed %d subscription(s), want 1", len(tr.subs))
	}

	delivery := context.WithValue(context.Background(), deliveryKey{}, "the broker's own record")
	if err := tr.subs[0].Handle(delivery, tr.msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if probe.got == nil {
		t.Fatal("the handler never ran")
	}
	if probe.got.Value(deliveryKey{}) != "the broker's own record" {
		t.Error("the handler's context carries nothing the delivery put there - the wrapper did not pass the delivery's context through")
	}
}
