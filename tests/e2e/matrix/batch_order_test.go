package matrix

import (
	"context"
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
