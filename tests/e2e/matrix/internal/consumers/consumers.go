// Package consumers is the application half of the fixture's event
// design: one struct per generated handler interface, and the groups this
// deployable consumes under.
//
// Nothing here is generated. The design declares the contracts and the
// handler interfaces; which broker identity each consume joins, and what
// the handler does, are the deployable's - so they are written by hand,
// in one place, the way a real project writes them.
package consumers

import (
	"context"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/events"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/eventsubs"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/events/upstream"
	eventtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/events"
	upstreamtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/upstream"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// The groups this deployable consumes under. One per service, because
// every service here consumes events.ItemStocked or another contract a
// sibling already consumes, and two subscriptions to one contract under
// one group are refused.
//
// AnalyticsGroup is the exception worth having: it covers two contracts,
// so the group is the unit of scaling rather than of subscription, and
// TrackTier pins the per-consume override by joining another.
const (
	InventoryGroup     craftevents.Group = "matrix-inventory"
	NotificationGroup  craftevents.Group = "matrix-notifications"
	OpsGroup           craftevents.Group = "matrix-ops"
	GuardedGroup       craftevents.Group = "matrix-guarded"
	LedgerGroup        craftevents.Group = "matrix-ledger"
	AnalyticsGroup     craftevents.Group = "analytics-worker"
	AnalyticsTierGroup craftevents.Group = "analytics-tier-worker"
)

// Inventory implements events.InventoryServiceHandler.
type Inventory struct{ SvcCtx *svccontext.ServiceContext }

func (h Inventory) MirrorStock(_ context.Context, payload *eventtypes.ItemStocked) error {
	h.SvcCtx.Record("MirrorStock", payload)
	return nil
}

// Notification implements eventsubs.NotificationServiceHandler, including
// the two consumes declared in `extend service` blocks.
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

// Ops implements eventsubs.OpsServiceHandler.
type Ops struct{ SvcCtx *svccontext.ServiceContext }

func (h Ops) RecordClosure(_ context.Context, payload *eventtypes.WarehouseClosed) error {
	h.SvcCtx.Record("RecordClosure", payload)
	return nil
}

// Analytics implements eventsubs.AnalyticsServiceHandler. TrackTier
// panics for one member id, which is the bug an application ships by
// accident - the fixture asserts the process survives it.
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

// Guarded implements eventsubs.GuardedServiceHandler.
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

// Ledger implements upstream.LedgerServiceHandler: the contract declared
// at file level, which this design consumes and never publishes.
type Ledger struct{ SvcCtx *svccontext.ServiceContext }

func (h Ledger) RecordSettlement(_ context.Context, payload *upstreamtypes.PaymentSettledPayload) error {
	h.SvcCtx.Record("RecordSettlement", payload)
	return nil
}

// RegisterAll registers every handler set this deployable runs, each
// behind chain. It is what a project's main.go writes once: the design
// says which handlers exist, this says which process runs them and under
// what identity.
//
// Nothing is delivered until [craftevents.Bus.Start].
func RegisterAll(bus *craftevents.Bus, svcCtx *svccontext.ServiceContext, chain craftevents.Chain) error {
	if err := events.RegisterInventoryServiceHandler(bus, Inventory{SvcCtx: svcCtx}, chain,
		events.InventoryServiceGroups{Default: InventoryGroup}); err != nil {
		return err
	}
	if err := eventsubs.RegisterNotificationServiceHandler(bus, Notification{SvcCtx: svcCtx}, chain,
		eventsubs.NotificationServiceGroups{Default: NotificationGroup}); err != nil {
		return err
	}
	if err := eventsubs.RegisterOpsServiceHandler(bus, Ops{SvcCtx: svcCtx}, chain,
		eventsubs.OpsServiceGroups{Default: OpsGroup}); err != nil {
		return err
	}
	if err := eventsubs.RegisterAnalyticsServiceHandler(bus, Analytics{SvcCtx: svcCtx}, chain,
		eventsubs.AnalyticsServiceGroups{Default: AnalyticsGroup, TrackTier: AnalyticsTierGroup}); err != nil {
		return err
	}
	if err := eventsubs.RegisterGuardedServiceHandler(bus, Guarded{SvcCtx: svcCtx}, chain,
		eventsubs.GuardedServiceGroups{Default: GuardedGroup}); err != nil {
		return err
	}
	return upstream.RegisterLedgerServiceHandler(bus, Ledger{SvcCtx: svcCtx}, chain,
		upstream.LedgerServiceGroups{Default: LedgerGroup})
}
