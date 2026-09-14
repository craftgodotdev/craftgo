package broker

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// A batch is not a transaction. When it stops partway the caller has to
// know WHICH entries did not go out, because resending one that did is a
// duplicate and skipping one that did not is a lost message.
//
// The entries that landed need not be the first: a broker publishing to
// several topics at once reports in completion order, so the gaps fall
// where the failures did. This reads those gaps off a real broker and
// then checks them against what the broker actually holds - a report can
// be internally consistent and still name the wrong entries, and nothing
// downstream can tell that happened.
func TestAPartialBatchNamesTheEnvelopesTheBrokerDidNotTake(t *testing.T) {
	// events.TierPromoted has no topic, so every TierPromoted entry fails
	// while its neighbours land.
	addrs := cluster(t, itemStocked, warehouseClosed, forged)
	svc := boot(t, addrs, false, nil, nil)

	stocked := func(sku string) *eventtypes.ItemStocked {
		return &eventtypes.ItemStocked{
			InventoryHeader: eventtypes.InventoryHeader{Sku: sku, Occurred: "2026-01-01T00:00:00Z"},
			Quantity:        1,
		}
	}
	promoted := &eventtypes.TierPromoted{Tier: 2, MemberID: "mem-1"}

	b := svc.Events.Batch()
	b.InventoryService().ItemStocked(stocked("sku-0"))
	b.InventoryService().TierPromoted(promoted)
	b.Craft().Forged(stocked("sku-2")) // a second service in the same batch
	b.InventoryService().TierPromoted(promoted)
	b.InventoryService().ItemStocked(stocked("sku-4"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := b.Publish(ctx)

	var partial *craftevents.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("publish error = %v (%T), want a *PartialPublishError", err, err)
	}
	if want := []int{1, 3}; !reflect.DeepEqual(partial.Unsent, want) {
		t.Errorf("Unsent = %v, want %v", partial.Unsent, want)
	}
	// Sent is the length of the leading run that landed, so a caller
	// resending envs[Sent:] starts at the first gap rather than past it.
	if partial.Sent != 1 {
		t.Errorf("Sent = %d, want 1", partial.Sent)
	}
	if partial.Event != tierPromoted {
		t.Errorf("Event = %q, want the contract of the first unsent entry (%q)", partial.Event, tierPromoted)
	}
	// A batch that failed is left intact, so nothing is lost by a caller
	// who reads the error before deciding what to do.
	if b.Len() != 5 {
		t.Errorf("batch holds %d entries after a partial failure, want 5", b.Len())
	}

	// What the broker holds is the assertion the report cannot fake. The
	// missing topic is created first, so its consumer subscribes to an
	// empty topic rather than a missing one.
	createTopic(t, addrs, tierPromoted)
	consumer := boot(t, addrs, true, nil, nil)

	landed := counts(consumer, "MirrorStock", "SendStockAlert", "CountStocked")
	waitFor(t, 60*time.Second, "the entries the report says landed",
		"MirrorStock=2 SendStockAlert=2 CountStocked=2", landed)
	stillTrue(t, 3*time.Second, "an entry the report named as unsent reached the broker",
		"TrackTier=0", counts(consumer, trackTier))

	for _, name := range []string{"MirrorStock", "SendStockAlert", "CountStocked"} {
		if got, want := skus(consumer.DeliveredTo(name)), []string{"sku-0", "sku-4"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s received %v, want %v", name, got, want)
		}
	}
}
