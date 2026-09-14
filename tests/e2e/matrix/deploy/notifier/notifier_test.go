// Package notifier is a PROJECTION of the matrix design: a deployable
// that runs only the consuming services, generated from a design folder
// it does not contain. Its manifest names the source with `design.from`
// and the services with `output.services`; the payload types and the
// event contracts it binds to are the ones the design's own project
// generates, shared rather than copied.
package notifier

import (
	"context"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"

	notifiertransport "github.com/craftgodotdev/craftgo/tests/e2e/matrix/deploy/notifier/internal/transport"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/deploy/notifier/svccontext"
	inventoryevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/inventory_service"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// boot wires this deployable's consumers onto an in-process bus. The
// publisher comes from the shared contract library - the projection
// never generates one, because it does not run the publishing service.
func boot(t *testing.T) (*svccontext.ServiceContext, *craftevents.Bus, *memory.Transport) {
	t.Helper()
	transport := memory.New(memory.WithErrorHandler(func(sub craftevents.Subscription, _ *craftevents.Message, err error) {
		t.Errorf("consumer %s/%s failed: %v", sub.Event, sub.Consumer, err)
	}))
	bus := craftevents.New(
		craftevents.WithTransport(transport),
		craftevents.WithCodec(codecjson.Codec{}),
	)
	svc := svccontext.NewServiceContext()
	svc.Events = svccontext.NewEvents(bus)
	if err := notifiertransport.SubscribeAll(context.Background(), bus, svc); err != nil {
		t.Fatalf("start consumers: %v", err)
	}
	return svc, bus, transport
}

// The deployable consumes contracts published by a service it does not
// run, through the library the design's own project generates.
func TestProjectionConsumesTheSharedContracts(t *testing.T) {
	svc, bus, transport := boot(t)
	publisher := inventoryevents.NewPublisher(bus)
	payload := &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-9", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        3,
	}
	if err := publisher.PublishItemStocked(context.Background(), payload); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	// Both selected services consume this contract, under their own
	// groups, so each receives its own copy.
	for _, consumer := range []string{"SendStockAlert", "CountStocked"} {
		got := svc.DeliveredTo(consumer)
		if len(got) != 1 {
			t.Fatalf("%s received %d payloads, want 1", consumer, len(got))
		}
		item, ok := got[0].(*eventtypes.ItemStocked)
		if !ok || item.Sku != "sku-9" || item.Quantity != 3 {
			t.Errorf("%s received %#v", consumer, got[0])
		}
	}
}

// A consumer an `extend service` block declares, and one a `@group`
// moved into another logic folder, are both part of the service this
// deployable runs - one handler set covers them.
func TestProjectionRunsEveryConsumerOfItsServices(t *testing.T) {
	svc, bus, transport := boot(t)
	publisher := inventoryevents.NewPublisher(bus)
	if err := publisher.PublishShipmentDispatched(context.Background(), &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-9", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := publisher.PublishStocktakeStarted(context.Background(), &eventtypes.StocktakeStarted{
		Warehouse: eventtypes.WarehouseNorth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	for _, consumer := range []string{"NotifyDispatch", "CountDispatched", "TrackStocktake"} {
		if got := svc.DeliveredTo(consumer); len(got) != 1 {
			t.Errorf("%s received %d payloads, want 1", consumer, len(got))
		}
	}
}

// The contract this deployable does not consume reaches nothing here,
// even though the design declares a consumer for it in a service the
// projection leaves out.
func TestProjectionLeavesTheServicesItDoesNotNameAlone(t *testing.T) {
	svc, bus, transport := boot(t)
	if err := inventoryevents.NewPublisher(bus).PublishWarehouseClosed(context.Background(), &eventtypes.WarehouseClosed{
		Warehouse: eventtypes.WarehouseSouth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	// OpsService consumes WarehouseClosed, and this deployable does not
	// run OpsService.
	if got := svc.DeliveredTo("RecordClosure"); len(got) != 0 {
		t.Errorf("a service the manifest does not name must not be subscribed, got %d payloads", len(got))
	}
}
