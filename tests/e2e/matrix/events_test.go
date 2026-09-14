package matrix

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"

	inventoryevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/inventory_service"
	notifyevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/notification_service"
	opsevents "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/ops_service"
	apptransport "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/transport"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/xshared"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// bootEvents wires the generated publishers and consumers onto an
// in-process bus. The transport is the only thing a project swaps to
// move onto a broker; nothing generated changes with it.
func bootEvents(t *testing.T) (*svccontext.ServiceContext, *memory.Transport) {
	t.Helper()
	return bootEventsWith(t, nil, nil)
}

// bootEventsWith boots the same wiring behind a consumer middleware chain
// and an error handler of the caller's choosing. The chain goes on the
// bus, so nothing generated knows it is there.
func bootEventsWith(t *testing.T, chain craftevents.Chain, onError func(craftevents.Subscription, error)) (*svccontext.ServiceContext, *memory.Transport) {
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
		craftevents.WithMiddleware(chain...),
	)
	svc := svccontext.NewServiceContext()
	svc.Events = svccontext.NewEvents(bus)
	if err := apptransport.SubscribeAll(context.Background(), bus, svc); err != nil {
		t.Fatalf("start consumers: %v", err)
	}
	return svc, transport
}

func TestEventReachesEveryConsumerOfTheContract(t *testing.T) {
	svc, transport := bootEvents(t)
	payload := &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        7,
	}
	if err := svc.Events.InventoryService.PublishItemStocked(context.Background(), payload); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	// The contract is consumed by the declaring service AND by a
	// consumer in another package; both receive it.
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

// A consumer declared in an `extend service` block and one behind a
// `@group` both register through the same umbrella.
func TestExtendAndGroupedConsumersAreRegistered(t *testing.T) {
	svc, transport := bootEvents(t)
	if err := svc.Events.InventoryService.PublishShipmentDispatched(context.Background(), &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-1", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := svc.Events.InventoryService.PublishWarehouseClosed(context.Background(), &eventtypes.WarehouseClosed{
		Warehouse: eventtypes.WarehouseNorth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	if got := svc.DeliveredTo("NotifyDispatch"); len(got) != 1 {
		t.Errorf("extend-block consumer received %d payloads, want 1", len(got))
	}
	if got := svc.DeliveredTo("RecordClosure"); len(got) != 1 {
		t.Errorf("grouped consumer received %d payloads, want 1", len(got))
	}
}

// NotificationService declares consumers in three blocks, one of them
// behind its own @group, so its logic is split across two packages. The
// contract still declares one handler interface, and the single handler
// set at the transport root satisfies it - every consumer receives.
func TestConsumersSplitAcrossGroupsShareOneHandlerSet(t *testing.T) {
	svc, transport := bootEvents(t)
	if err := svc.Events.InventoryService.PublishStocktakeStarted(context.Background(), &eventtypes.StocktakeStarted{
		Warehouse: eventtypes.WarehouseNorth,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := svc.Events.InventoryService.PublishShipmentDispatched(context.Background(), &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-2", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	if got := svc.DeliveredTo("TrackStocktake"); len(got) != 1 {
		t.Errorf("consumer grouped into its own package received %d payloads, want 1", len(got))
	}
	if got := svc.DeliveredTo("NotifyDispatch"); len(got) != 1 {
		t.Errorf("ungrouped sibling received %d payloads, want 1", len(got))
	}
}

// `@contract` fixes the wire identity; the consumer that names the event
// by its DSL name still receives it.
func TestContractOverrideIsTheWireIdentity(t *testing.T) {
	svc, transport := bootEvents(t)
	payload := &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-9", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        1,
	}
	if err := svc.Events.InventoryService.PublishReconciled(context.Background(), payload); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()
	if got := svc.DeliveredTo("AuditReconciliation"); len(got) != 1 {
		t.Fatalf("renamed contract delivered %d payloads, want 1", len(got))
	}
	// The contract the consumer subscribed to is the overridden name, not
	// the derived one.
	if got := svc.DeliveredTo("SendStockAlert"); len(got) != 0 {
		t.Errorf("the renamed contract must not reach ItemStocked consumers, got %d", len(got))
	}
}

// The message key is a publish-time value: it reaches the transport
// because the caller passed WithKey, and a publish without one is
// keyless. Nothing in the design decides it.
func TestPublishOptionsSetTheMessageKey(t *testing.T) {
	var seen []*craftevents.Message
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) { seen = append(seen, m) }}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))
	pub := svccontext.NewEvents(bus).InventoryService

	ctx := context.Background()
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1"}}
	if err := pub.PublishItemStocked(ctx, stocked, craftevents.WithKey(string(stocked.Sku))); err != nil {
		t.Fatal(err)
	}
	// An int-valued enum has no key form of its own any more - the caller
	// renders it however its broker wants it.
	closed := &eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseSouth}
	if err := pub.PublishWarehouseClosed(ctx, closed,
		craftevents.WithKey(strconv.FormatInt(int64(closed.Warehouse), 10)),
		craftevents.WithDedupID("dedup-1"),
		craftevents.WithHeader("tenant", "acme")); err != nil {
		t.Fatal(err)
	}
	if err := pub.PublishStocktakeStarted(ctx, &eventtypes.StocktakeStarted{Warehouse: eventtypes.WarehouseNorth}); err != nil {
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

// A publisher's defaults are applied to every message it sends, and a
// per-call option of the same kind replaces one. The batch builder
// carries the same defaults, so the two ways to publish agree.
func TestPublisherDefaultsApplyAndPerCallOptionsWin(t *testing.T) {
	var seen []*craftevents.Message
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) { seen = append(seen, m) }}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))
	events := svccontext.NewEvents(bus,
		craftevents.WithHeader("tenant", "acme"),
		craftevents.WithKey("default-key"))

	ctx := context.Background()
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-2"}}
	if err := events.InventoryService.PublishItemStocked(ctx, stocked); err != nil {
		t.Fatal(err)
	}
	if err := events.InventoryService.PublishItemStocked(ctx, stocked, craftevents.WithKey("sku-2")); err != nil {
		t.Fatal(err)
	}
	b := events.Batch()
	b.InventoryService().ItemStocked(stocked)
	b.InventoryService().ItemStocked(stocked, craftevents.WithKey("sku-3"))
	// A second service's builder, which the caller never handed the
	// defaults to, carries them too.
	b.Craft().Forged(stocked)
	if err := b.Publish(ctx); err != nil {
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

// An option addressed to the transport that is actually wired up, under a
// key it does not read, fails the publish. Dropping it silently is how a
// message goes out configured differently from how its caller asked.
func TestAnUnknownOptionInTheAdaptersOwnNamespaceFailsThePublish(t *testing.T) {
	transport := memory.New()
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	pub := svccontext.NewEvents(bus).InventoryService
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-4"}}

	err := pub.PublishItemStocked(context.Background(), stocked,
		craftevents.WithAdapterOption(memory.Adapter, "partition", 3))
	var unknown *craftevents.UnknownOptionError
	if !errors.As(err, &unknown) {
		t.Fatalf("publish error = %v, want *UnknownOptionError", err)
	}
	if unknown.Adapter != memory.Adapter || unknown.Key != "partition" {
		t.Errorf("error names %s/%s", unknown.Adapter, unknown.Key)
	}

	// The same option under another adapter's name is that adapter's
	// business, so this transport lets it by.
	if err := pub.PublishItemStocked(context.Background(), stocked,
		craftevents.WithAdapterOption("kafka", "timestamp", "whenever")); err != nil {
		t.Errorf("another adapter's option must be ignored, got %v", err)
	}
}

// An invalid payload is rejected at the consumer, before logic sees it -
// the event-side counterpart of the HTTP handler's bind-then-validate.
func TestConsumerValidatesBeforeLogic(t *testing.T) {
	var mu sync.Mutex
	var failed error
	transport := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		// The contract has consumers in two groups, so the handler runs
		// on one delivery goroutine per group.
		mu.Lock()
		failed = err
		mu.Unlock()
	}))
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	svc := svccontext.NewServiceContext()
	svc.Events = svccontext.NewEvents(bus)
	if err := apptransport.SubscribeAll(context.Background(), bus, svc); err != nil {
		t.Fatalf("start consumers: %v", err)
	}
	// carrier is @minLength(1); an empty one must not reach logic.
	if err := svc.Events.InventoryService.PublishShipmentDispatched(context.Background(), &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-2",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()
	mu.Lock()
	defer mu.Unlock()
	if failed == nil {
		t.Fatal("expected the consumer to reject the invalid payload")
	}
	// The error names the contract it arrived on: a generated Validate
	// reports field-scoped text, which on its own says nothing about
	// which event an operator is reading about.
	if !strings.Contains(failed.Error(), "events.ShipmentDispatched") {
		t.Errorf("validation failure does not name the contract: %v", failed)
	}
	if got := svc.DeliveredTo("NotifyDispatch"); len(got) != 0 {
		t.Errorf("invalid payload reached logic: %#v", got)
	}
}

// recordingTransport is a publish-only transport, standing in for a
// broker adapter: the generated publisher needs nothing but the two
// interface methods.
type recordingTransport struct {
	onPublish func(*craftevents.Message)
}

func (r *recordingTransport) Publish(_ context.Context, msg *craftevents.Message) error {
	r.onPublish(msg)
	return nil
}

// A batch may carry contracts from several services - the shape an outbox
// drains - and reaches the transport in one call.
func TestEventBatchMixesContracts(t *testing.T) {
	var mu sync.Mutex
	var got []string
	tr := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		t.Errorf("consumer failed: %v", err)
	}))
	bus := craftevents.New(craftevents.WithTransport(tr), craftevents.WithCodec(codecjson.Codec{}))
	for _, contract := range []string{"events.ItemStocked", "events.WarehouseClosed"} {
		if err := bus.Subscribe(context.Background(), craftevents.Subscription{
			Event: contract, Consumer: "Probe",
			Handle: func(_ context.Context, msg *craftevents.Message) error {
				mu.Lock()
				got = append(got, msg.Event+"|"+msg.Key)
				mu.Unlock()
				return nil
			},
		}); err != nil {
			t.Fatalf("subscribe %s: %v", contract, err)
		}
	}

	evts := svccontext.NewEvents(bus)
	b := evts.Batch()
	b.InventoryService().ItemStocked(&eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1", Occurred: "now"}, Quantity: 3},
		craftevents.WithKey("sku-1"))
	b.InventoryService().WarehouseClosed(&eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth},
		craftevents.WithKey("1"))
	if b.Len() != 2 {
		t.Fatalf("batch holds %d events, want 2", b.Len())
	}
	if err := b.Publish(context.Background()); err != nil {
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

// One consumer group spanning several contracts: AnalyticsService's
// service-level @consumerGroup puts CountStocked and CountDispatched in
// one group, and each still receives the contract it declared - the
// group is the unit of scaling, not a filter.
func TestOneGroupSpansSeveralContracts(t *testing.T) {
	svc, transport := bootEvents(t)
	if err := svc.Events.InventoryService.PublishItemStocked(context.Background(), &eventtypes.ItemStocked{
		InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-3", Occurred: "2026-01-01T00:00:00Z"},
		Quantity:        2,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := svc.Events.InventoryService.PublishShipmentDispatched(context.Background(), &eventtypes.ShipmentDispatched{
		ShipmentID: "shp-3", Carrier: "acme",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := svc.Events.InventoryService.PublishTierPromoted(context.Background(), &eventtypes.TierPromoted{
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

// The group is resolved once, in the design, and emitted verbatim. The
// derived form qualifies the consumer with its package and service;
// @group (output layout) and the extend block that declared a consumer
// contribute nothing to it.
func TestSubscriptionGroupsAreTheDesignedOnes(t *testing.T) {
	want := map[string]string{
		"SendStockAlert":      "eventsubs-NotificationService-SendStockAlert",
		"AuditReconciliation": "eventsubs-NotificationService-AuditReconciliation",
		"NotifyDispatch":      "eventsubs-NotificationService-NotifyDispatch",
		"TrackStocktake":      "eventsubs-NotificationService-TrackStocktake",
	}
	bus := craftevents.New(craftevents.WithCodec(codecjson.Codec{}))
	for _, sub := range notifyevents.Subscriptions(bus, apptransport.NewNotificationServiceConsumers(svccontext.NewServiceContext())) {
		if got := sub.Group; got != want[sub.Consumer] {
			t.Errorf("%s group = %q, want %q", sub.Consumer, got, want[sub.Consumer])
		}
	}

	// OpsService carries @group("ops"), which decides only where its files
	// land; RecordClosure joins the same group it would without it.
	for _, sub := range opsevents.Subscriptions(bus, apptransport.NewOpsServiceConsumers(svccontext.NewServiceContext())) {
		if got, want := sub.Group, "eventsubs-OpsService-RecordClosure"; got != want {
			t.Errorf("%s group = %q, want %q", sub.Consumer, got, want)
		}
	}
}

// panickingOps is an OpsService handler set that panics, standing in for
// the bug an application ships by accident.
type panickingOps struct{}

func (panickingOps) RecordClosure(context.Context, *eventtypes.WarehouseClosed) error {
	panic("consumer exploded")
}

// A panicking consumer must not take the process down - the API and every
// other consumer run in the same binary. The guard is on the subscription
// the Bus registers, so the generated consumer inherits it.
func TestPanickingConsumerDoesNotEndTheProcess(t *testing.T) {
	var mu sync.Mutex
	var failed []error
	transport := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		mu.Lock()
		failed = append(failed, err)
		mu.Unlock()
	}))
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	if err := bus.SubscribeAll(context.Background(), opsevents.Subscriptions(bus, panickingOps{})); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	publisher := inventoryevents.NewPublisher(bus)
	closed := &eventtypes.WarehouseClosed{Warehouse: eventtypes.WarehouseNorth}
	for i := 0; i < 2; i++ {
		if err := publisher.PublishWarehouseClosed(context.Background(), closed); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	// Both messages were delivered: the panic ends one delivery, not the
	// subscription and not the process.
	if len(failed) != 2 {
		t.Fatalf("error handler saw %d failures, want 2: %v", len(failed), failed)
	}
	var pe *craftevents.PanicError
	if !errors.As(failed[0], &pe) {
		t.Fatalf("recovered panic is not a *PanicError: %#v", failed[0])
	}
	if pe.Consumer != "RecordClosure" || pe.Group != "eventsubs-OpsService-RecordClosure" {
		t.Errorf("panic error does not name the designed subscription: %+v", pe)
	}
}

// A payload that does not decode never reaches logic, and the error says
// which contract it arrived on.
func TestUndecodablePayloadNeverReachesLogic(t *testing.T) {
	var mu sync.Mutex
	var failed error
	transport := memory.New(memory.WithErrorHandler(func(_ craftevents.Subscription, _ *craftevents.Message, err error) {
		mu.Lock()
		failed = err
		mu.Unlock()
	}))
	bus := craftevents.New(craftevents.WithTransport(transport), craftevents.WithCodec(codecjson.Codec{}))
	svc := svccontext.NewServiceContext()
	svc.Events = svccontext.NewEvents(bus)
	if err := apptransport.SubscribeAll(context.Background(), bus, svc); err != nil {
		t.Fatalf("start consumers: %v", err)
	}
	// Published by something that is not this design - a truncated body
	// under a contract craftgo consumes.
	if err := transport.Publish(context.Background(), &craftevents.Message{
		Event:    "events.WarehouseClosed",
		Payload:  []byte("{"),
		Metadata: map[string]string{craftevents.MetaCodec: "json"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.Drain()

	mu.Lock()
	defer mu.Unlock()
	if failed == nil {
		t.Fatal("expected the consumer to reject the malformed payload")
	}
	if !strings.Contains(failed.Error(), "events.WarehouseClosed") {
		t.Errorf("decode failure does not name the contract: %v", failed)
	}
	if got := svc.DeliveredTo("RecordClosure"); len(got) != 0 {
		t.Errorf("malformed payload reached logic: %#v", got)
	}
}
