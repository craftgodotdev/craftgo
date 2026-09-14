package consumers

import (
	"context"
	"fmt"

	"github.com/craftgodotdev/craftgo/example/brokers/internal/types/orders"
)

// Counter implements analytics.AnalyticsServiceHandler: a second group on
// orders.Placed, receiving its own copy of every order.
type Counter struct{}

func (Counter) CountOrder(_ context.Context, payload *orders.OrderPlaced) error {
	fmt.Printf("  [analytics/CountOrder]        order=%s total=%d\n", payload.OrderID, payload.Total)
	return nil
}
