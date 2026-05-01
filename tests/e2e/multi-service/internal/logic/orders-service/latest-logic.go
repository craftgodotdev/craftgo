package ordersservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/multi-service/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/multi-service/svccontext"
)

// LatestLogic carries per-request state for OrdersService.Latest.
type LatestLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewLatestLogic returns a fresh logic instance.
func NewLatestLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *LatestLogic {
	return &LatestLogic{ctx: ctx, svcCtx: svcCtx}
}

// Latest reads the seeded most-recent order from svcCtx.
func (l *LatestLogic) Latest() (*types.Order, error) {
	return &types.Order{ID: l.svcCtx.OrderID, Total: l.svcCtx.OrderTotal}, nil
}
