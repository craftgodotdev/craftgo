package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"
	"github.com/craftgodotdev/craftgo/pkg/wire"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/upstream"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	upstreamtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/upstream"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/xshared"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// bootEvents starts this deployable's consumers on the memory transport.
func bootEvents(t *testing.T) (*svccontext.ServiceContext, *craftevents.Bus, *memory.Transport) {
	t.Helper()
	return bootEventsWith(t, nil, nil)
}

// bootEventsWith is bootEvents with a bus-wide chain and a transport error
// handler; a nil onError fails the test on any consumer error.
func bootEventsWith(t *testing.T, chain craftevents.Chain, onError func(craftevents.Subscription, error)) (*svccontext.ServiceContext, *craftevents.Bus, *memory.Transport) {
	t.Helper()
	if onError == nil {
		onError = func(sub craftevents.Subscription, err error) {
			t.Errorf("consumer %s/%s failed: %v", sub.Event, sub.Consumer, err)
		}
	}
	transport := memory.New(memory.WithErrorHandler(func(sub craftevents.Subscription, _ *craftevents.Message, err error) {
		onError(sub, err)
	}))
	bus := craftevents.New(
		craftevents.WithTransport(transport),
		craftevents.WithCodec(codecjson.Codec{}),
	)
	bus.Use(chain...)
	svc := svccontext.NewServiceContext()
	if err := consumers.Register(bus, svc); err != nil {
		t.Fatalf("register consumers: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start consumers: %v", err)
	}
	return svc, bus, transport
}

func TestEventReachesEveryListenerOfTheContract(t *testing.T) {
	svc, bus, transport := bootEvents(t)
	payload := &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        7,
	}
	if err := events.ItemStocked.Publish(context.Background(), bus, payload); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	// Two of its four listeners, in different modules and groups.
	for _, consumer := range []string{"MirrorStock", "SendStockAlert"} {
		got := svc.DeliveredTo(consumer)
		if len(got) != 1 {
			t.Fatalf("%s received %d payloads, want 1", consumer, len(got))
		}
		item, ok := got[0].(*eventtypes.ItemStocked)
		if !ok || item.Sku != "sku-1" || item.Quantity != 7 {
			t.Errorf("%s received %#v", consumer, got[0])
		}
	}
}

// A one-subscription module and a four-subscription module both receive.
func TestEveryRegisteredModuleReceives(t *testing.T) {
	svc, bus, transport := bootEvents(t)
	if err := events.ShipmentDispatched.Publish(context.Background(), bus, &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-1", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := events.WarehouseClosed.Publish(context.Background(), bus, &eventtypes.WarehouseClosed{
		Warehouse: eventtypes.WarehouseNorth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	if got := svc.DeliveredTo("NotifyDispatch"); len(got) != 1 {
		t.Errorf("a listener sharing its group with three others received %d payloads, want 1", len(got))
	}
	if got := svc.DeliveredTo("RecordClosure"); len(got) != 1 {
		t.Errorf("a module with one subscription received %d payloads, want 1", len(got))
	}
}

// A module listening to four contracts under one group receives each on the
// subscription that names it.
func TestOneModuleReceivesEveryContractItListensTo(t *testing.T) {
	svc, bus, transport := bootEvents(t)
	if err := events.StocktakeStarted.Publish(context.Background(), bus, &eventtypes.StocktakeStarted{
		Warehouse: eventtypes.WarehouseNorth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := events.ShipmentDispatched.Publish(context.Background(), bus, &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-2", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	if got := svc.DeliveredTo("TrackStocktake"); len(got) != 1 {
		t.Errorf("the module's third subscription received %d payloads, want 1", len(got))
	}
	if got := svc.DeliveredTo("NotifyDispatch"); len(got) != 1 {
		t.Errorf("its sibling under the same group received %d payloads, want 1", len(got))
	}
}

// A `@contract` name is the wire identity its listeners subscribe to.
func TestContractOverrideIsTheWireIdentity(t *testing.T) {
	svc, bus, transport := bootEvents(t)
	payload := &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-9", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}
	if events.ReconciledContract != "legacy.inventory.reconciled.v2" {
		t.Fatalf("the descriptor carries %q, want the @contract override", events.ReconciledContract)
	}
	if err := events.Reconciled.Publish(context.Background(), bus, payload); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()
	if got := svc.DeliveredTo("AuditReconciliation"); len(got) != 1 {
		t.Fatalf("renamed contract delivered %d payloads, want 1", len(got))
	}
	// Reconciled shares ItemStocked's payload type, not its contract.
	if got := svc.DeliveredTo("SendStockAlert"); len(got) != 0 {
		t.Errorf("the renamed contract must not reach ItemStocked listeners, got %d", len(got))
	}
}

// The key, dedup ID and headers come from publish options; with no WithKey
// the message is keyless.
func TestPublishOptionsSetTheMessageKey(t *testing.T) {
	var seen []*craftevents.Message
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) { seen = append(seen, m) }}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))

	ctx := context.Background()
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1"}}
	if err := events.ItemStocked.Publish(ctx, bus, stocked, craftevents.WithKey(string(stocked.Sku))); err != nil {
		t.Fatal(err)
	}
	// An int enum has no key form; the caller renders one.
	closed := &eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseSouth}
	if err := events.WarehouseClosed.Publish(ctx, bus, closed,
		craftevents.WithKey(strconv.FormatInt(int64(closed.Warehouse), 10)),
		craftevents.WithDedupID("dedup-1"),
		craftevents.WithHeader("tenant", "acme")); err != nil {
		t.Fatal(err)
	}
	if err := events.StocktakeStarted.Publish(ctx, bus, &eventtypes.StocktakeStarted{Warehouse: eventtypes.WarehouseNorth}); err != nil {
		t.Fatal(err)
	}
	want := []struct{ event, key string }{
		{"events.ItemStocked", "sku-1"},
		{"events.WarehouseClosed", "2"},
		{"events.StocktakeStarted", ""},
	}
	if len(seen) != len(want) {
		t.Fatalf("published %d messages, want %d", len(seen), len(want))
	}
	for i, w := range want {
		if seen[i].Event != w.event || seen[i].Key != w.key {
			t.Errorf("message %d = {%s %q}, want {%s %q}", i, seen[i].Event, seen[i].Key, w.event, w.key)
		}
		if got := seen[i].Metadata[craftevents.MetaCodec]; got != "json" {
			t.Errorf("message %d codec = %q", i, got)
		}
	}
	if seen[1].DedupID != "dedup-1" {
		t.Errorf("dedup id = %q", seen[1].DedupID)
	}
	if got := seen[1].Metadata["tenant"]; got != "acme" {
		t.Errorf("header = %q", got)
	}
}

// Bus publish defaults reach every message, single or batched, and a
// per-call option or an envelope's own Key wins over them.
func TestBusPublishDefaultsApplyAndPerCallOptionsWin(t *testing.T) {
	var seen []*craftevents.Message
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) { seen = append(seen, m) }}
	bus := craftevents.New(
		craftevents.WithPublisher(&recorder),
		craftevents.WithCodec(codecjson.Codec{}),
		craftevents.WithPublishDefaults(
			craftevents.WithHeader("tenant", "acme"),
			craftevents.WithKey("default-key")))

	ctx := context.Background()
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-2"}}
	if err := events.ItemStocked.Publish(ctx, bus, stocked); err != nil {
		t.Fatal(err)
	}
	if err := events.ItemStocked.Publish(ctx, bus, stocked, craftevents.WithKey("sku-2")); err != nil {
		t.Fatal(err)
	}
	// The third entry is a second contract of the same package.
	if err := bus.PublishAll(ctx, []craftevents.Envelope{
		{Event: events.ItemStockedContract, Payload: stocked},
		{Event: events.ItemStockedContract, Key: "sku-3", Payload: stocked},
		{Event: events.ForgedContract, Payload: stocked},
	}); err != nil {
		t.Fatal(err)
	}

	wantKeys := []string{"default-key", "sku-2", "default-key", "sku-3", "default-key"}
	if len(seen) != len(wantKeys) {
		t.Fatalf("published %d messages, want %d", len(seen), len(wantKeys))
	}
	for i, want := range wantKeys {
		if seen[i].Key != want {
			t.Errorf("message %d key = %q, want %q", i, seen[i].Key, want)
		}
		if got := seen[i].Metadata["tenant"]; got != "acme" {
			t.Errorf("message %d lost the default header: %v", i, seen[i].Metadata)
		}
	}
}

// An option under the wired adapter's name, with a key it does not read,
// fails the publish.
func TestAnUnknownOptionInTheAdaptersOwnNamespaceFailsThePublish(t *testing.T) {
	transport := memory.New()
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-4"}}

	err := events.ItemStocked.Publish(context.Background(), bus, stocked,
		craftevents.WithAdapterOption(memory.Adapter, "partition", 3))
	var unknown *craftevents.UnknownOptionError
	if !errors.As(err, &unknown) {
		t.Fatalf("publish error = %v, want *UnknownOptionError", err)
	}
	if unknown.Adapter != memory.Adapter || unknown.Key != "partition" {
		t.Errorf("error names %s/%s", unknown.Adapter, unknown.Key)
	}

	if err := events.ItemStocked.Publish(context.Background(), bus, stocked,
		craftevents.WithAdapterOption("kafka", "timestamp", "whenever")); err != nil {
		t.Errorf("another adapter's option must be ignored, got %v", err)
	}
}

// A listener rejects an invalid payload before logic sees it.
func TestListenerValidatesBeforeLogic(t *testing.T) {
	var mu sync.Mutex
	var failed error
	svc, _, transport := bootEventsWith(t, nil, func(_ craftevents.Subscription, err error) {
		// Called from one delivery goroutine per group.
		mu.Lock()
		failed = err
		mu.Unlock()
	})
	// carrier is @minLength(1). The descriptor's Publish would refuse an
	// empty one, so it goes on the transport directly.
	body, err := json.Marshal(&eventtypes.ShipmentDispatched{ShipmentID: "shp-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Publish(context.Background(), &craftevents.Message{
		Event:    events.ShipmentDispatchedContract,
		Payload:  body,
		Metadata: map[string]string{craftevents.MetaCodec: "json"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()
	mu.Lock()
	defer mu.Unlock()
	if failed == nil {
		t.Fatal("expected the listener to reject the invalid payload")
	}
	if !strings.Contains(failed.Error(), "events.ShipmentDispatched") {
		t.Errorf("validation failure does not name the contract: %v", failed)
	}
	if got := svc.DeliveredTo("NotifyDispatch"); len(got) != 0 {
		t.Errorf("invalid payload reached logic: %#v", got)
	}
}

// A descriptor's Publish refuses an invalid payload with a *PayloadError
// before the transport sees it.
func TestPublisherValidatesBeforeAnythingIsSent(t *testing.T) {
	var published int
	recorder := recordingTransport{onPublish: func(*craftevents.Message) { published++ }}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))

	err := events.ShipmentDispatched.Publish(context.Background(), bus,
		&eventtypes.ShipmentDispatched{ShipmentID: "shp-3"})
	var payload *craftevents.PayloadError
	if !errors.As(err, &payload) {
		t.Fatalf("publish error = %v (%T), want a *PayloadError", err, err)
	}
	if payload.Event != events.ShipmentDispatchedContract {
		t.Errorf("the failure names %q, want the contract", payload.Event)
	}
	if published != 0 {
		t.Errorf("an invalid payload reached the transport %d time(s)", published)
	}
}

// recordingTransport is a publish-only transport feeding onPublish.
type recordingTransport struct {
	onPublish func(*craftevents.Message)
}

func (r *recordingTransport) Publish(_ context.Context, msg *craftevents.Message) error {
	r.onPublish(msg)
	return nil
}

// A batch mixing contracts delivers each entry to its contract's listener.
func TestEventBatchMixesContracts(t *testing.T) {
	var mu sync.Mutex
	var got []string
	tr := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		t.Errorf("consumer failed: %v", err)
	}))
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	for _, contract := range []string{events.ItemStockedContract, events.WarehouseClosedContract} {
		if err := bus.Register(craftevents.Subscription{
			Event: contract, Consumer: "Probe", Group: "probe",
			Handle: func(_ context.Context, msg *craftevents.Message) error {
				mu.Lock()
				got = append(got, msg.Event+"|"+msg.Key)
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("register %s: %v", contract, err)
		}
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := bus.PublishAll(context.Background(), []craftevents.Envelope{
		{
			Event: events.ItemStockedContract, Key: "sku-1",
			Payload: &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "now"}, Quantity: 3},
		},
		{
			Event: events.WarehouseClosedContract, Key: "1",
			Payload: &eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth},
		},
	}); err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	tr.Drain()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"events.ItemStocked|sku-1", "events.WarehouseClosed|1"}
	if len(got) != len(want) {
		t.Fatalf("delivered %v, want %v", got, want)
	}
	sort.Strings(got)
	sort.Strings(want)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delivered %v, want %v", got, want)
			break
		}
	}
}

// Analytics receives on all three subscriptions: two share analytics-worker
// and TrackTier has a group of its own.
func TestOneGroupSpansSeveralContracts(t *testing.T) {
	svc, bus, transport := bootEvents(t)
	if err := events.ItemStocked.Publish(context.Background(), bus, &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-3", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        2,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := events.ShipmentDispatched.Publish(context.Background(), bus, &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-3", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := events.TierPromoted.Publish(context.Background(), bus, &eventtypes.TierPromoted{
		Tier: xshared.XTierGold, MemberID: "mem-1",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	for _, consumer := range []string{"CountStocked", "CountDispatched", "TrackTier"} {
		if got := svc.DeliveredTo(consumer); len(got) != 1 {
			t.Errorf("%s received %d payloads, want 1", consumer, len(got))
		}
	}
}

// The plan consumers.Register produces matches testdata/plan.json.
func TestThePlanIsTheDeployablesOwnShape(t *testing.T) {
	bus := craftevents.New(craftevents.WithTransport(memory.New()), craftevents.WithCodec(codecjson.Codec{}))
	if err := consumers.Register(bus, svccontext.NewServiceContext()); err != nil {
		t.Fatalf("register consumers: %v", err)
	}
	got, err := json.MarshalIndent(bus.Plan(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "plan.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("bus.Plan() drifted from %s:\n%s", golden, got)
	}
}

// A subscription with no group is refused with ErrNoGroup naming its
// contract, and the subscriptions beside it still register.
func TestRegisterRefusesASubscriptionWithNoGroup(t *testing.T) {
	bus := craftevents.New(craftevents.WithTransport(memory.New()), craftevents.WithCodec(codecjson.Codec{}))
	guarded := consumers.Guarded{SvcCtx: svccontext.NewServiceContext()}
	err := errors.Join(
		events.ItemStocked.Subscribe(bus, consumers.GuardedGroup, guarded.GuardedStock),
		events.StocktakeStarted.Subscribe(bus, "", guarded.BareStock),
		events.WarehouseClosed.Subscribe(bus, consumers.GuardedGroup, guarded.InheritedStock),
	)
	if err == nil {
		t.Fatal("registered a subscription with no group")
	}
	if !errors.Is(err, craftevents.ErrNoGroup) {
		t.Errorf("registration failed with %v, want ErrNoGroup", err)
	}
	if !strings.Contains(err.Error(), events.StocktakeStartedContract) {
		t.Errorf("the refusal does not name the contract of the line that broke: %v", err)
	}
	groups := bus.Plan().Groups
	if len(groups) != 1 {
		t.Fatalf("the plan holds %d groups, want 1: %+v", len(groups), groups)
	}
	var got []string
	for _, consumer := range groups[0].Consumers {
		got = append(got, consumer.Event)
	}
	want := events.ItemStockedContract + "," + events.WarehouseClosedContract
	if strings.Join(got, ",") != want {
		t.Errorf("registered %v, want %s - a refusal must not take the lines beside it with it", got, want)
	}
}

// A listener panic fails its delivery with a *PanicError, and the next
// delivery still runs.
func TestPanickingListenerDoesNotEndTheProcess(t *testing.T) {
	var mu sync.Mutex
	var failed []error
	transport := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		mu.Lock()
		failed = append(failed, err)
		mu.Unlock()
	}))
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	if err := events.WarehouseClosed.Subscribe(bus, consumers.OpsGroup,
		panickingOps{}.RecordClosure); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	closed := &eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth}
	for i := 0; i < 2; i++ {
		if err := events.WarehouseClosed.Publish(context.Background(), bus, closed); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	// Each panic ends one delivery, not the subscription.
	if len(failed) != 2 {
		t.Fatalf("error handler saw %d failures, want 2: %v", len(failed), failed)
	}
	var pe *craftevents.PanicError
	if !errors.As(failed[0], &pe) {
		t.Fatalf("recovered panic is not a *PanicError: %#v", failed[0])
	}
	// Event and Group identify a registration; Consumer defaults to Event.
	if pe.Event != events.WarehouseClosedContract || pe.Group != consumers.OpsGroup {
		t.Errorf("panic error does not name the registered subscription: %+v", pe)
	}
}

// panickingOps is a WarehouseClosed listener that panics.
type panickingOps struct{}

func (panickingOps) RecordClosure(context.Context, *eventtypes.WarehouseClosed) error {
	panic("consumer exploded")
}

// A payload that does not decode never reaches logic, and the error says
// which contract it arrived on.
func TestUndecodablePayloadNeverReachesLogic(t *testing.T) {
	var mu sync.Mutex
	var failed error
	svc, _, transport := bootEventsWith(t, nil, func(_ craftevents.Subscription, err error) {
		mu.Lock()
		failed = err
		mu.Unlock()
	})
	// A truncated body, put on the transport directly.
	if err := transport.Publish(context.Background(), &craftevents.Message{
		Event:    events.WarehouseClosedContract,
		Payload:  []byte("{"),
		Metadata: map[string]string{craftevents.MetaCodec: "json"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	if failed == nil {
		t.Fatal("expected the listener to reject the malformed payload")
	}
	if !strings.Contains(failed.Error(), "events.WarehouseClosed") {
		t.Errorf("decode failure does not name the contract: %v", failed)
	}
	if got := svc.DeliveredTo("RecordClosure"); len(got) != 0 {
		t.Errorf("malformed payload reached logic: %#v", got)
	}
}

// A `payload T[]` contract delivers the whole slice to one handler call.
func TestAnArrayPayloadContractRoundTripsABatch(t *testing.T) {
	transport := memory.New()
	bus := craftevents.New(
		craftevents.WithTransport(transport),
		craftevents.WithCodec(codecjson.Codec{}),
	)
	got := make(chan *[]upstreamtypes.PaymentSettledPayload, 1)
	if err := upstream.PaymentsSettledBatch.Subscribe(bus, "settlements",
		func(_ context.Context, batch *[]upstreamtypes.PaymentSettledPayload) error {
			got <- batch
			return nil
		}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	batch := &[]upstreamtypes.PaymentSettledPayload{
		{InvoiceID: "inv-1", Amount: 100},
		{InvoiceID: "inv-2", Amount: 250},
	}
	if err := upstream.PaymentsSettledBatch.Publish(context.Background(), bus, batch); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	select {
	case delivered := <-got:
		if len(*delivered) != 2 || (*delivered)[1].InvoiceID != "inv-2" {
			t.Errorf("delivered %+v", *delivered)
		}
	default:
		t.Fatal("nothing delivered")
	}
}

// Publish validates every element of a `payload T[]` contract; a broken one
// fails it with a *PayloadError naming the element.
func TestAnArrayPayloadValidatesEveryElement(t *testing.T) {
	bus := craftevents.New(
		craftevents.WithTransport(memory.New()),
		craftevents.WithCodec(codecjson.Codec{}),
	)
	err := upstream.PaymentsSettledBatch.Publish(context.Background(), bus,
		&[]upstreamtypes.PaymentSettledPayload{
			{InvoiceID: "inv-1", Amount: 100},
			{InvoiceID: "", Amount: -1},
		})
	var payloadErr *craftevents.PayloadError
	if !errors.As(err, &payloadErr) {
		t.Fatalf("err = %T %v, want *PayloadError", err, err)
	}
	if payloadErr.Event != upstream.PaymentsSettledBatchContract {
		t.Errorf("the error does not name the contract: %+v", payloadErr)
	}
	if !strings.Contains(err.Error(), "item 1") || !strings.Contains(err.Error(), "invoiceId") {
		t.Errorf("the error names neither the element nor the field: %v", err)
	}
}

// A `bytes @format(raw)` payload field reaches the consumer byte for byte:
// raw's null, past-2^53 integer and 1.50 would each change through `any`.
func TestARawPayloadFieldReachesTheConsumerUnchanged(t *testing.T) {
	const raw = `{"explicit":null,"big":12345678901234567890,"trailing":1.50}`

	got := make(chan *eventtypes.WarehouseClosed, 1)
	tr := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		t.Errorf("consumer failed: %v", err)
	}))
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	if err := events.WarehouseClosed.Subscribe(bus, consumers.OpsGroup,
		func(_ context.Context, closed *eventtypes.WarehouseClosed) error {
			got <- closed
			return nil
		}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := events.WarehouseClosed.Publish(context.Background(), bus, &eventtypes.WarehouseClosed{
		Warehouse: eventtypes.WarehouseNorth,
		Details:   wire.Raw(raw),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tr.Drain()

	select {
	case closed := <-got:
		if closed.Details == nil {
			t.Fatal("the consumer received no details at all")
		}
		if string(closed.Details) != raw {
			t.Errorf("details arrived as %s, want the published bytes %s", closed.Details, raw)
		}
	default:
		t.Fatal("nothing was delivered")
	}
}

// An unset optional raw field is left out of the JSON, not written as null.
func TestAnAbsentRawFieldIsNotWritten(t *testing.T) {
	body, err := json.Marshal(&eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "details") {
		t.Errorf("an unset optional raw field reached the wire: %s", body)
	}
}
