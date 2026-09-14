package matrix

import (
	"context"
	"errors"
	"reflect"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// A batch collects the contracts of several services into ONE list, in
// the order the caller added them, whichever service's builder each entry
// came from. The builders are values the caller may hold, so an entry
// added through one after another service has added its own still lands
// where it was written.
//
// The order is not cosmetic: a partial failure names the entries that did
// not go out BY INDEX, so an entry that moved is an entry the caller
// retries in place of another - see TestAPartialBatchNamesTheEnvelopes-
// TheBrokerDidNotTake, which reads those indices off a real broker.
func TestABatchKeepsTheOrderEntriesWereAddedAcrossServices(t *testing.T) {
	var seen []string
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) {
		seen = append(seen, m.Event+"|"+m.Key)
	}}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))
	events := svccontext.NewEvents(bus)

	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1"}}
	b := events.Batch()
	// Both builders are taken up front and used alternately, which is the
	// shape an outbox drain has: one pass over rows of mixed kinds.
	inventory, craft := b.InventoryService(), b.Craft()
	inventory.ItemStocked(stocked, craftevents.WithKey("k-0"))
	craft.Forged(stocked, craftevents.WithKey("k-1"))
	inventory.WarehouseClosed(&eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth},
		craftevents.WithKey("k-2"))
	craft.Forged(stocked, craftevents.WithKey("k-3"))

	if b.Len() != 4 {
		t.Fatalf("batch holds %d entries, want 4 - a held builder collected into a list of its own", b.Len())
	}
	if err := b.Publish(context.Background()); err != nil {
		t.Fatalf("publish: %v", err)
	}

	want := []string{
		"events.ItemStocked|k-0",
		"events.Forged|k-1",
		"events.WarehouseClosed|k-2",
		"events.Forged|k-3",
	}
	if len(seen) != len(want) {
		t.Fatalf("published %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("published\n%v\nwant\n%v", seen, want)
		}
	}
}

// stoppingTransport is a publish-only transport that fails on the nth
// message, standing in for a broker that takes some of a batch and then
// stops. It has no PublishBatch, so the bus publishes one at a time -
// which is the path that reports a contiguous tail.
type stoppingTransport struct {
	failAt int
	sent   []string
}

func (s *stoppingTransport) Publish(_ context.Context, msg *craftevents.Message) error {
	if len(s.sent) == s.failAt {
		return errors.New("broker refused the record")
	}
	s.sent = append(s.sent, msg.Event)
	return nil
}

// A batch that stops partway names the entries that did not go out, on
// the ONE-AT-A-TIME path as well as the batched one. A transport with no
// batch upgrade stops at the first failure, so what did not go out is a
// contiguous tail - and the caller retries envs[Sent:] off the same two
// fields either way, without having to know which path ran.
//
// Understating that tail is the one failure a caller cannot detect for
// itself: it reads the missing entries as delivered and never sends them
// again. TestAPartialBatchNamesTheEnvelopesTheBrokerDidNotTake reads the
// scattered shape of this off a real broker; this reads the contiguous
// one, which no broker is needed to produce.
func TestABatchThatStopsPartwayNamesTheTailThatDidNotGoOut(t *testing.T) {
	recorder := &stoppingTransport{failAt: 2}
	bus := craftevents.New(craftevents.WithPublisher(recorder), craftevents.WithCodec(codecjson.Codec{}))
	events := svccontext.NewEvents(bus)

	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1"}}
	b := events.Batch()
	b.InventoryService().ItemStocked(stocked)
	b.Craft().Forged(stocked)
	b.InventoryService().WarehouseClosed(&eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth})
	b.Craft().Forged(stocked)

	var partial *craftevents.PartialPublishError
	if err := b.Publish(context.Background()); !errors.As(err, &partial) {
		t.Fatalf("publish error = %v (%T), want a *PartialPublishError", err, err)
	}
	if want := []int{2, 3}; !reflect.DeepEqual(partial.Unsent, want) {
		t.Errorf("Unsent = %v, want %v", partial.Unsent, want)
	}
	if partial.Sent != 2 {
		t.Errorf("Sent = %d, want 2", partial.Sent)
	}
	if want := "events.WarehouseClosed"; partial.Event != want {
		t.Errorf("Event = %q, want the contract of the first unsent entry (%q)", partial.Event, want)
	}
	// What the transport was actually handed, which the report cannot
	// fake: it stopped at the failure rather than carrying on past it, so
	// the tail the report names is the tail that did not go out.
	if want := []string{"events.ItemStocked", "events.Forged"}; !reflect.DeepEqual(recorder.sent, want) {
		t.Errorf("the transport was handed %v, want %v", recorder.sent, want)
	}
	// A batch that failed is left intact, so nothing is lost by a caller
	// who reads the error before deciding what to do.
	if b.Len() != 4 {
		t.Errorf("batch holds %d entries after a partial failure, want 4", b.Len())
	}
}
