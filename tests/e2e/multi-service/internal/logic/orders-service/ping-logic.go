// Package ordersservice carries the business logic for OrdersService.
// The multi-service e2e fixture proves two services can coexist in one
// project and route to distinct handler/logic packages without collision.
package ordersservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/multi-service/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/multi-service/svccontext"
)

// PingLogic carries per-request state for OrdersService.Ping.
type PingLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewPingLogic returns a fresh logic instance.
func NewPingLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *PingLogic {
	return &PingLogic{ctx: ctx, svcCtx: svcCtx}
}

// Ping returns a fixed Pong identifying the orders service.
func (l *PingLogic) Ping() (*types.Pong, error) {
	return &types.Pong{Name: "orders"}, nil
}
