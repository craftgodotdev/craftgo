package matrix

import (
	"context"
	"errors"
	"reflect"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
)

// mixedBatch returns four envelopes interleaving three contracts of one
// package; keys[i] keys entry i.
func mixedBatch(keys ...string) []craftevents.Envelope {
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1"}}
	closed := &eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth}
	envs := []craftevents.Envelope{
		{Event: events.ItemStockedContract, Payload: stocked},
		{Event: events.ForgedContract, Payload: stocked},
		{Event: events.WarehouseClosedContract, Payload: closed},
		{Event: events.ForgedContract, Payload: stocked},
	}
	for i := range keys {
		envs[i].Key = keys[i]
	}
	return envs
}

// A batch reaches the transport in the order the caller assembled it,
// whichever contract each entry carries.
func TestABatchKeepsTheOrderEntriesWereAddedAcrossContracts(t *testing.T) {
	var seen []string
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) {
		seen = append(seen, m.Event+"|"+m.Key)
	}}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))

	if err := bus.PublishAll(context.Background(), mixedBatch("k-0", "k-1", "k-2", "k-3")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	want := []string{
		"events.ItemStocked|k-0",
		"events.Forged|k-1",
		"events.WarehouseClosed|k-2",
		"events.Forged|k-3",
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("published\n%v\nwant\n%v", seen, want)
	}
}

// stoppingTransport publishes until failAt messages are sent, then refuses.
// It has no PublishBatch, so the bus publishes one message at a time.
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

// On the one-at-a-time path, a batch that stops partway names the unsent tail.
func TestABatchThatStopsPartwayNamesTheTailThatDidNotGoOut(t *testing.T) {
	recorder := &stoppingTransport{failAt: 2}
	bus := craftevents.New(craftevents.WithPublisher(recorder), craftevents.WithCodec(codecjson.Codec{}))

	envs := mixedBatch()
	var partial *craftevents.PartialPublishError
	if err := bus.PublishAll(context.Background(), envs); !errors.As(err, &partial) {
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
	// The transport holds exactly the entries the report counts as sent.
	if want := []string{"events.ItemStocked", "events.Forged"}; !reflect.DeepEqual(recorder.sent, want) {
		t.Errorf("the transport was handed %v, want %v", recorder.sent, want)
	}
	if len(envs) != 4 {
		t.Errorf("batch holds %d entries after a partial failure, want 4", len(envs))
	}
}
