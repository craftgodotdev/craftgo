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

// deliveryKey is a context key only the test's delivery context carries.
type deliveryKey struct{}

// handingTransport keeps the subscriptions and the message the bus hands it,
// for a test to deliver by hand.
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

// contextProbe records the context MirrorStock is called with.
type contextProbe struct{ got context.Context }

func (p *contextProbe) MirrorStock(ctx context.Context, _ *eventtypes.ItemStocked) error {
	p.got = ctx
	return nil
}

// The descriptor's wrapper passes the delivery's context to the handler.
func TestTheDeliveryContextReachesTheHandler(t *testing.T) {
	tr := &handingTransport{}
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))

	probe := &contextProbe{}
	if err := events.ItemStocked.Subscribe(bus, consumers.InventoryGroup,
		probe.MirrorStock); err != nil {
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
