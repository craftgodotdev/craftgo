// Package consumers is what this deployable listens to. The design
// declares the contracts and nothing else; which of them a process
// subscribes to, under which broker identity, and what runs on a
// delivery are Go, written here.
//
// One struct per module, and one Register joining every subscription as
// a line: `<pkg>.<Event>.Subscribe(bus, group, method)`. The compiler
// checks each line - a method whose payload does not match the contract
// does not compile at the Subscribe call - and testdata/plan.json pins
// the set.
package consumers

import (
	"context"
	"errors"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/upstream"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	upstreamtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/upstream"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// The groups this deployable consumes under. One per module, because
// every module here listens to events.ItemStocked or another contract a
// sibling already listens to, and two subscriptions to one contract
// under one group are refused.
//
// AnalyticsGroup is the exception worth having: it covers two contracts,
// so the group is the unit of scaling rather than of subscription, and
// TrackTier joins another to show one module spanning two.
const (
	InventoryGroup     craftevents.Group = "matrix-inventory"
	NotificationGroup  craftevents.Group = "matrix-notifications"
	OpsGroup           craftevents.Group = "matrix-ops"
	GuardedGroup       craftevents.Group = "matrix-guarded"
	LedgerGroup        craftevents.Group = "matrix-ledger"
	AnalyticsGroup     craftevents.Group = "analytics-worker"
	AnalyticsTierGroup craftevents.Group = "analytics-tier-worker"
)

// Inventory listens to the contract declared in its own package.
type Inventory struct{ SvcCtx *svccontext.ServiceContext }

func (h Inventory) MirrorStock(_ context.Context, payload *eventtypes.ItemStocked) error {
	h.SvcCtx.Record("MirrorStock", payload)
	return nil
}

// Notification listens to four contracts under one group, one of them by
// its @contract-renamed wire identity.
type Notification struct{ SvcCtx *svccontext.ServiceContext }

func (h Notification) SendStockAlert(_ context.Context, payload *eventtypes.ItemStocked) error {
	h.SvcCtx.Record("SendStockAlert", payload)
	return nil
}

func (h Notification) AuditReconciliation(_ context.Context, payload *eventtypes.ItemStocked) error {
	h.SvcCtx.Record("AuditReconciliation", payload)
	return nil
}

func (h Notification) NotifyDispatch(_ context.Context, payload *eventtypes.ShipmentDispatched) error {
	h.SvcCtx.Record("NotifyDispatch", payload)
	return nil
}

func (h Notification) TrackStocktake(_ context.Context, payload *eventtypes.StocktakeStarted) error {
	h.SvcCtx.Record("TrackStocktake", payload)
	return nil
}

// Ops is the second listener of events.WarehouseClosed, under a group of
// its own, so one publish drives two different tasks.
type Ops struct{ SvcCtx *svccontext.ServiceContext }

func (h Ops) RecordClosure(_ context.Context, payload *eventtypes.WarehouseClosed) error {
	h.SvcCtx.Record("RecordClosure", payload)
	return nil
}

// Analytics spans two groups. TrackTier panics for one member id, which
// is the bug an application ships by accident - the fixture asserts the
// process survives it.
type Analytics struct{ SvcCtx *svccontext.ServiceContext }

func (h Analytics) CountStocked(_ context.Context, payload *eventtypes.ItemStocked) error {
	h.SvcCtx.Record("CountStocked", payload)
	return nil
}

func (h Analytics) CountDispatched(_ context.Context, payload *eventtypes.ShipmentDispatched) error {
	h.SvcCtx.Record("CountDispatched", payload)
	return nil
}

func (h Analytics) TrackTier(_ context.Context, payload *eventtypes.TierPromoted) error {
	if payload.MemberID == "panic-please" {
		panic("consumer exploded")
	}
	h.SvcCtx.Record("TrackTier", payload)
	return nil
}

// Guarded is the module whose delivery the middleware tests decorate.
type Guarded struct{ SvcCtx *svccontext.ServiceContext }

func (h Guarded) GuardedStock(_ context.Context, payload *eventtypes.ItemStocked) error {
	h.SvcCtx.Record("GuardedStock", payload)
	return nil
}

func (h Guarded) BareStock(_ context.Context, payload *eventtypes.StocktakeStarted) error {
	h.SvcCtx.Record("BareStock", payload)
	return nil
}

func (h Guarded) InheritedStock(_ context.Context, payload *eventtypes.WarehouseClosed) error {
	h.SvcCtx.Record("InheritedStock", payload)
	return nil
}

// Ledger listens to the contract declared in a package with no service
// at all, which this design describes and never publishes.
type Ledger struct{ SvcCtx *svccontext.ServiceContext }

func (h Ledger) RecordSettlement(_ context.Context, payload *upstreamtypes.PaymentSettledPayload) error {
	h.SvcCtx.Record("RecordSettlement", payload)
	return nil
}

// Register registers every subscription this deployable runs. It is
// what a project's main.go calls once, after putting its delivery chain
// on the bus with [craftevents.Bus.Use]: the design says which contracts
// exist, this says which of them this process listens to and under what
// identity.
//
// The lines are joined rather than chained, so every one is offered to
// the bus and every refusal - each naming its contract and group -
// reaches the caller. Nothing is delivered until [craftevents.Bus.Start].
func Register(bus *craftevents.Bus, svcCtx *svccontext.ServiceContext) error {
	inventory := Inventory{SvcCtx: svcCtx}
	notification := Notification{SvcCtx: svcCtx}
	ops := Ops{SvcCtx: svcCtx}
	analytics := Analytics{SvcCtx: svcCtx}
	guarded := Guarded{SvcCtx: svcCtx}
	ledger := Ledger{SvcCtx: svcCtx}

	return errors.Join(
		events.ItemStocked.Subscribe(bus, InventoryGroup, inventory.MirrorStock),

		events.ItemStocked.Subscribe(bus, NotificationGroup, notification.SendStockAlert),
		events.Reconciled.Subscribe(bus, NotificationGroup, notification.AuditReconciliation),
		events.ShipmentDispatched.Subscribe(bus, NotificationGroup, notification.NotifyDispatch),
		events.StocktakeStarted.Subscribe(bus, NotificationGroup, notification.TrackStocktake),

		events.WarehouseClosed.Subscribe(bus, OpsGroup, ops.RecordClosure),

		events.ItemStocked.Subscribe(bus, AnalyticsGroup, analytics.CountStocked),
		events.ShipmentDispatched.Subscribe(bus, AnalyticsGroup, analytics.CountDispatched),
		events.TierPromoted.Subscribe(bus, AnalyticsTierGroup, analytics.TrackTier),

		events.ItemStocked.Subscribe(bus, GuardedGroup, guarded.GuardedStock),
		events.StocktakeStarted.Subscribe(bus, GuardedGroup, guarded.BareStock),
		events.WarehouseClosed.Subscribe(bus, GuardedGroup, guarded.InheritedStock),

		upstream.PaymentSettled.Subscribe(bus, LedgerGroup, ledger.RecordSettlement),
	)
}
