package consumers

import (
	"context"
	"fmt"

	"github.com/craftgodotdev/craftgo/example/brokers/internal/types/orders"
	"github.com/craftgodotdev/craftgo/example/brokers/internal/types/payments"
)

// Ledger is the ledger group's logic: a third group on orders.Placed,
// and the only listener of the upstream payment contract - so it is where
// the two meet.
type Ledger struct{}

func (Ledger) BookOrder(_ context.Context, payload *orders.OrderPlaced) error {
	fmt.Printf("  [ledger/BookOrder]            order=%s total=%d\n", payload.OrderID, payload.Total)
	return nil
}

func (Ledger) RecordSettlement(_ context.Context, payload *payments.Settlement) error {
	fmt.Printf("  [ledger/RecordSettlement]     order=%s amount=%d (upstream contract)\n", payload.OrderID, payload.Amount)
	return nil
}
