package consumers

import (
	"context"
	"fmt"

	"github.com/craftgodotdev/craftgo/example/brokers/internal/types/orders"
)

// Notifier implements notifications.NotificationServiceHandler: it
// follows an order through both of its events.
//
// The payload arrives decoded and validated, whichever broker delivered
// it; returning an error tells the transport the message was not
// processed.
type Notifier struct{}

func (Notifier) SendReceipt(_ context.Context, payload *orders.OrderPlaced) error {
	fmt.Printf("  [notifications/SendReceipt]   order=%s total=%d\n", payload.OrderID, payload.Total)
	return nil
}

func (Notifier) SendDispatchNote(_ context.Context, payload *orders.OrderShipped) error {
	fmt.Printf("  [notifications/SendDispatch]  order=%s carrier=%s\n", payload.OrderID, payload.Carrier)
	return nil
}
