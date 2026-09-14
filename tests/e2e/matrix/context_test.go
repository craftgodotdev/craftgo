package matrix

import (
	"context"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	inventoryevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/inventory_service"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// deliveryKey marks a value only the transport put on the context, so a
// handler reading it back proves the context it was called with is the
// one the delivery carried.
type deliveryKey struct{}

// handingTransport keeps the subscription the bus registered and the
// message the bus encoded, then hands the one to the other on a context
// of its own. It stands in for what a broker adapter reaches a handler
// with - its own record, a header this application did not map, a trace
// it opened - none of which [craftevents.Message] carries.
type handingTransport struct {
	sub craftevents.Subscription
	msg *craftevents.Message
}

func (h *handingTransport) Subscribe(_ context.Context, sub craftevents.Subscription) error {
	h.sub = sub
	return nil
}

func (h *handingTransport) Publish(_ context.Context, msg *craftevents.Message) error {
	h.msg = msg
	return nil
}

// contextProbe is a handler set satisfying the contract package's own
// Consumers interface, standing in for the generated one so the context a
// handler is called with can be read.
type contextProbe struct{ got context.Context }

func (p *contextProbe) MirrorStock(ctx context.Context, _ *eventtypes.ItemStocked) error {
	p.got = ctx
	return nil
}

// The context a consumer is called with is the DELIVERY's, not one the
// wrapper made up. That is the whole reach a consumer has to what
// [craftevents.Message] does not carry, and a wrapper that passed a fresh
// context instead would take it away with every handler still compiling
// and every payload still arriving.
//
// The subscription is built by the generated contract package, so what
// this pins is that package's wrapper: decode, validate, dispatch, and
// the context going through all three untouched. That a Kafka delivery
// carries its record at all is the adapter's own business and is pinned
// there, so nothing here needs a broker.
func TestTheDeliveryContextReachesTheHandlerSet(t *testing.T) {
	tr := &handingTransport{}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))

	probe := &contextProbe{}
	if err := bus.SubscribeAll(context.Background(), inventoryevents.Subscriptions(bus, probe)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := inventoryevents.NewPublisher(bus).PublishItemStocked(context.Background(), &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}, craftevents.WithKey("sku-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if tr.msg == nil {
		t.Fatal("the transport was handed no message to deliver")
	}

	delivery := context.WithValue(context.Background(), deliveryKey{}, "the broker's own record")
	if err := tr.sub.Handle(delivery, tr.msg); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if probe.got == nil {
		t.Fatal("the consumer never ran")
	}
	if probe.got.Value(deliveryKey{}) != "the broker's own record" {
		t.Error("the consumer's context carries nothing the delivery put there - the wrapper did not pass the delivery's context through")
	}
}
