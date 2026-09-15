// Package consumers is what this binary listens to: the broker identity
// each subscription joins, and the one call that registers them all.
//
// Nothing here is generated. The design declares the contracts; which of
// them a process listens to, under which group, and what runs on a
// delivery are this deployable's - so they are Go, written where its bus
// is built.
package consumers

import (
	"errors"

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

// Register binds every subscription this binary runs to bus: one line
// per (contract, group), each naming the method it dispatches to. The
// compiler checks the pairing - a method whose payload does not match the
// contract does not compile at the Subscribe call.
//
// The lines are joined, so every one is offered to the bus and each
// refusal names the contract and group it was on. Nothing is delivered
// until [craftevents.Bus.Start].
func Register(bus *craftevents.Bus) error {
	notifier, counter, ledger := Notifier{}, Counter{}, Ledger{}
	return errors.Join(
		orders.Placed.Subscribe(bus, NotificationGroup, notifier.SendReceipt),
		orders.Shipped.Subscribe(bus, NotificationGroup, notifier.SendDispatchNote),

		orders.Placed.Subscribe(bus, AnalyticsGroup, counter.CountOrder),

		orders.Placed.Subscribe(bus, LedgerGroup, ledger.BookOrder),
		payments.Settled.Subscribe(bus, LedgerGroup, ledger.RecordSettlement),
	)
}
