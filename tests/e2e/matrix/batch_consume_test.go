package matrix

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// A generated contract consumes in batches through its descriptor: the valid payloads
// reach the batch function typed, and one that fails validation is named by its index in
// the error the transport reports once.
func TestAContractConsumesInBatches(t *testing.T) {
	var mu sync.Mutex
	var reported []error
	transport := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, msg *craftevents.Message, err error) {
		mu.Lock()
		defer mu.Unlock()
		if msg != nil {
			t.Errorf("a batch error arrived with message %+v", msg)
		}
		reported = append(reported, err)
	}))
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	var skus []string
	if err := events.ItemStocked.SubscribeBatch(bus, "stock-bulk", craftevents.BatchSize{Max: 3, Wait: time.Hour},
		func(_ context.Context, batch []craftevents.Item[eventtypes.ItemStocked]) error {
			mu.Lock()
			defer mu.Unlock()
			for _, it := range batch {
				skus = append(skus, it.Payload.Sku)
			}
			return nil
		}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bus.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	stocked := func(sku string, qty int32) *eventtypes.ItemStocked {
		return &eventtypes.ItemStocked{
			InventoryHeader: eventtypes.InventoryHeader{Sku: sku, Occurred: "2026-01-01T00:00:00Z"},
			Quantity:        qty,
		}
	}
	for _, p := range []*eventtypes.ItemStocked{stocked("a", 1), stocked("b", 2)} {
		if err := events.ItemStocked.Publish(ctx, bus, p); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	// The bus publishes what the descriptor would refuse.
	if err := bus.Publish(ctx, events.ItemStockedContract, stocked("bad", -1)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	if len(skus) != 2 {
		t.Errorf("batch function saw %v, want the two valid payloads", skus)
	}
	if len(reported) != 1 {
		t.Fatalf("reported %v, want one batch error", reported)
	}
	var itemErrs craftevents.ItemErrors
	var pe *craftevents.PayloadError
	if !errors.As(reported[0], &itemErrs) || len(itemErrs) != 1 || !errors.As(reported[0], &pe) {
		t.Errorf("reported %v, want ItemErrors naming the invalid payload", reported[0])
	}
}
