// Package consumers is what this binary listens to: the broker identity
// each subscription joins, and the one call that registers them all.
//
// Nothing here is generated. The design declares the contracts; which of
// them a process listens to, under which group, and what runs on a
// delivery are this deployable's - so they are Go, written where its bus
// is built.
package consumers

import (
	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	"github.com/craftgodotdev/craftgo/example/brokers/internal/events/orders"
	"github.com/craftgodotdev/craftgo/example/brokers/internal/events/payments"
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

// RegisterAll binds every subscription this binary runs to bus: one line
// per (contract, group), each naming the method it dispatches to. The
// compiler checks the pairing - a method whose payload does not match the
// contract does not compile at the Subscription call.
//
// The whole set goes over in one [craftevents.Bus.RegisterAll], so a
// refusal names the contract and group of the line that broke. Nothing is
// delivered until [craftevents.Bus.Start].
func RegisterAll(bus *craftevents.Bus) error {
	notifier, counter, ledger := Notifier{}, Counter{}, Ledger{}
	return bus.RegisterAll(
		orders.Placed.Subscription(bus, NotificationGroup, notifier.SendReceipt),
		orders.Shipped.Subscription(bus, NotificationGroup, notifier.SendDispatchNote),

		orders.Placed.Subscription(bus, AnalyticsGroup, counter.CountOrder),

		orders.Placed.Subscription(bus, LedgerGroup, ledger.BookOrder),
		payments.Settled.Subscription(bus, LedgerGroup, ledger.RecordSettlement),
	)
}
