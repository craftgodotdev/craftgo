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

// mixedBatch is one drain of an outbox: contracts from two services,
// interleaved, each entry keyed by the entity it is about.
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
//
// The order is not cosmetic: a partial failure names the entries that did
// not go out BY INDEX, so an entry that moved is an entry the caller
// retries in place of another - see TestAPartialBatchNamesTheEnvelopes-
// TheBrokerDidNotTake, which reads those indices off a real broker.
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
	// What the transport was actually handed, which the report cannot
	// fake: it stopped at the failure rather than carrying on past it, so
	// the tail the report names is the tail that did not go out.
	if want := []string{"events.ItemStocked", "events.Forged"}; !reflect.DeepEqual(recorder.sent, want) {
		t.Errorf("the transport was handed %v, want %v", recorder.sent, want)
	}
	// The caller's batch is left intact, so nothing is lost by one who
	// reads the error before deciding what to do.
	if len(envs) != 4 {
		t.Errorf("batch holds %d entries after a partial failure, want 4", len(envs))
	}
}
