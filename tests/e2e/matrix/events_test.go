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

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/consumers"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/xshared"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// bootEvents registers every subscription this fixture runs on an
// in-process bus and starts it. The transport is the only thing a project
// swaps to move onto a broker; nothing generated changes with it.
func bootEvents(t *testing.T) (*svccontext.ServiceContext, *craftevents.Bus, *memory.Transport) {
	t.Helper()
	return bootEventsWith(t, nil, nil)
}

// bootEventsWith boots the same wiring behind a bus-wide middleware chain
// and an error handler of the caller's choosing. The chain is installed
// with [craftevents.Bus.Use] after the bus exists, which is where a
// deployable builds one out of its own service context - and it covers
// every subscription registered through the bus, so no registration call
// knows it is there.
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

	// The contract has listeners in two modules of this deployable,
	// under two groups; both receive it.
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

// Every module registered from the one Register call receives, so a
// contract with a single listener and one with several are wired by the
// same list of lines.
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

// One module listens to four contracts under a single group, and each
// line receives the contract it names: the group is the unit of scaling,
// not a filter. Two of the four are published here; the other two are
// the renamed contract and the one every module listens to.
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

// `@contract` fixes the wire identity; the listener that reaches the
// event through its descriptor still receives it.
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
	// The contract the listener subscribed to is the overridden name, not
	// the derived one.
	if got := svc.DeliveredTo("SendStockAlert"); len(got) != 0 {
		t.Errorf("the renamed contract must not reach ItemStocked listeners, got %d", len(got))
	}
}

// The message key is a publish-time value: it reaches the transport
// because the caller passed WithKey, and a publish without one is
// keyless. Nothing in the design decides it.
func TestPublishOptionsSetTheMessageKey(t *testing.T) {
	var seen []*craftevents.Message
	recorder := recordingTransport{onPublish: func(m *craftevents.Message) { seen = append(seen, m) }}
	bus := craftevents.New(craftevents.WithPublisher(&recorder), craftevents.WithCodec(codecjson.Codec{}))

	ctx := context.Background()
	stocked := &eventtypes.ItemStocked{InventoryHeader: eventtypes.InventoryHeader{Sku: "sku-1"}}
	if err := events.ItemStocked.Publish(ctx, bus, stocked, craftevents.WithKey(string(stocked.Sku))); err != nil {
		t.Fatal(err)
	}
	// An int-valued enum has no key form of its own - the caller renders
	// it however its broker wants it.
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

// A bus-wide publish default is applied to every message sent through it,
// and a per-call option of the same kind replaces one. A batch carries
// the same defaults, so the two ways to publish agree.
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
	// A batch the caller assembles by hand: the entry that names a key
	// keeps it, the one that does not takes the bus default. The third
	// entry is a second contract of the same package.
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

// An option addressed to the transport that is actually wired up, under a
// key it does not read, fails the publish. Dropping it silently is how a
// message goes out configured differently from how its caller asked.
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

	// The same option under another adapter's name is that adapter's
	// business, so this transport lets it by.
	if err := events.ItemStocked.Publish(context.Background(), bus, stocked,
		craftevents.WithAdapterOption("kafka", "timestamp", "whenever")); err != nil {
		t.Errorf("another adapter's option must be ignored, got %v", err)
	}
}

// An invalid payload is rejected at the listener, before logic sees it -
// the event-side counterpart of the HTTP handler's bind-then-validate.
func TestListenerValidatesBeforeLogic(t *testing.T) {
	var mu sync.Mutex
	var failed error
	svc, _, transport := bootEventsWith(t, nil, func(_ craftevents.Subscription, err error) {
		// The contract has listeners in several groups, so the handler
		// runs on one delivery goroutine per group.
		mu.Lock()
		failed = err
		mu.Unlock()
	})
	// carrier is @minLength(1); an empty one must not reach logic. The
	// publisher validates too, so the message is put on the transport
	// directly - the way another system's would arrive.
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

// The publisher validates too, so a payload that cannot satisfy its
// contract is refused where it is broken rather than at every consumer.
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

// recordingTransport is a publish-only transport, standing in for a
// broker adapter: a descriptor needs nothing but the one interface
// method.
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

// One group spanning several contracts: Analytics puts two of its lines
// in one group, and the third in another because its delivery is read on
// its own elsewhere. All three receive - which group a line joins is
// written beside it, and nothing about it reaches the design.
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

// The groups are the APPLICATION's, and no generated file states what
// this deployable listens to - so the deployable pins its own shape with
// a golden plan. A subscription that moved group, a module that stopped
// being registered, or a contract renamed underneath one all show up
// here as a diff.
//
// Consumer is the contract on every line, because [craftevents.Event.Subscription]
// defaults it there: what tells four listeners of events.ItemStocked
// apart in this process is the group each joined.
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

// A line left without a group is refused at registration, naming the
// contract it is on: a group is where a listener resumes, so it is the
// application's to choose rather than something to fall back into.
//
// The lines are joined, so the refusal names the line that broke and the
// lines beside it are registered all the same.
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
	// Only the line that broke was refused: the other two are on the bus,
	// so what the error reports is the whole of what went wrong.
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

// A panicking listener must not take the process down - the API and every
// other listener run in the same binary. The guard is on the subscription
// the Bus registers, so every registered handler inherits it.
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
	// Both messages were delivered: the panic ends one delivery, not the
	// subscription and not the process.
	if len(failed) != 2 {
		t.Fatalf("error handler saw %d failures, want 2: %v", len(failed), failed)
	}
	var pe *craftevents.PanicError
	if !errors.As(failed[0], &pe) {
		t.Fatalf("recovered panic is not a *PanicError: %#v", failed[0])
	}
	// Event and Group are the pair that identifies a registration -
	// Consumer defaults to the contract, so it says the same thing.
	if pe.Event != events.WarehouseClosedContract || pe.Group != consumers.OpsGroup {
		t.Errorf("panic error does not name the registered subscription: %+v", pe)
	}
}

// panickingOps is a listener that panics, standing in for the bug an
// application ships by accident.
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
	// Published by something that is not this design - a truncated body
	// under a contract craftgo consumes.
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
