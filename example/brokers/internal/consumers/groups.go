// Package consumers is the application side of the design: one struct per
// generated handler interface, the broker identity each joins, and the
// registration that binds them to a bus.
//
// Nothing here is generated. The design declares the contracts and the
// handler interfaces; which group a consumer resumes under, and what it
// does with a payload, are this deployable's.
package consumers

import (
	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	"github.com/craftgodotdev/craftgo/example/brokers/internal/events/analytics"
	"github.com/craftgodotdev/craftgo/example/brokers/internal/events/ledger"
	"github.com/craftgodotdev/craftgo/example/brokers/internal/events/notifications"
)

// The three groups this binary consumes under. Each is a separate broker
// identity, so every publish of orders.Placed is delivered three times -
// once per group. Replicas of one deployable share these names and divide
// the work instead.
//
// Written down once, here, because a group is where a consumer resumes:
// on Kafka and JetStream the name IS the stored position, so it outlives
// any one process.
const (
	NotificationGroup craftevents.Group = "brokers-notifications"
	AnalyticsGroup    craftevents.Group = "brokers-analytics"
	LedgerGroup       craftevents.Group = "brokers-ledger"
)

// RegisterAll binds every handler set this binary runs to bus, each
// behind chain. Nothing is delivered until [craftevents.Bus.Start].
func RegisterAll(bus *craftevents.Bus, chain craftevents.Chain) error {
	if err := notifications.RegisterNotificationServiceHandler(bus, Notifier{}, chain,
		notifications.NotificationServiceGroups{Default: NotificationGroup}); err != nil {
		return err
	}
	if err := analytics.RegisterAnalyticsServiceHandler(bus, Counter{}, chain,
		analytics.AnalyticsServiceGroups{Default: AnalyticsGroup}); err != nil {
		return err
	}
	return ledger.RegisterLedgerServiceHandler(bus, Ledger{}, chain,
		ledger.LedgerServiceGroups{Default: LedgerGroup})
}
