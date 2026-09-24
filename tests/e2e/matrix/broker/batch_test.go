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

// A batch that stops partway on a real broker names exactly the entries the
// broker did not take, which need not be a tail.
func TestAPartialBatchNamesTheEnvelopesTheBrokerDidNotTake(t *testing.T) {
	// events.TierPromoted has no topic, so its entries fail while the rest land.
	addrs := cluster(t, itemStocked, warehouseClosed, forged)
	_, bus := boot(t, addrs, false, nil, nil)

	stocked := func(sku string) *eventtypes.ItemStocked {
		return &eventtypes.ItemStocked{
			InventoryHeader: eventtypes.InventoryHeader{Sku: sku, Occurred: "2026-01-01T00:00:00Z"},
			Quantity:        1,
		}
	}
	promoted := &eventtypes.TierPromoted{Tier: 2, MemberID: "mem-1"}

	envs := []craftevents.Envelope{
		{Event: itemStocked, Payload: stocked("sku-0")},
		{Event: tierPromoted, Payload: promoted},
		{Event: forged, Payload: stocked("sku-2")}, // a second contract over the ItemStocked payload
		{Event: tierPromoted, Payload: promoted},
		{Event: itemStocked, Payload: stocked("sku-4")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := bus.PublishAll(ctx, envs)

	var partial *craftevents.PartialPublishError
	if !errors.As(err, &partial) {
		t.Fatalf("publish error = %v (%T), want a *PartialPublishError", err, err)
	}
	if want := []int{1, 3}; !reflect.DeepEqual(partial.Unsent, want) {
		t.Errorf("Unsent = %v, want %v", partial.Unsent, want)
	}
	// Sent counts the leading run that landed.
	if partial.Sent != 1 {
		t.Errorf("Sent = %d, want 1", partial.Sent)
	}
	if partial.Event != tierPromoted {
		t.Errorf("Event = %q, want the contract of the first unsent entry (%q)", partial.Event, tierPromoted)
	}
	if len(envs) != 5 {
		t.Errorf("batch holds %d entries after a partial failure, want 5", len(envs))
	}

	// Read back what the broker holds, creating the missing topic first so
	// the consumer has a topic to subscribe to.
	createTopic(t, addrs, tierPromoted)
	consumer, _ := boot(t, addrs, true, nil, nil)

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
