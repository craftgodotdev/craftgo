package orderservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/orders"
	shared "github.com/craftgodotdev/craftgo/example/internal/types/shared"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// DeleteOrderService carries the per-request state for the DeleteOrder
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type DeleteOrderService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDeleteOrderService constructs a fresh service instance bound to ctx.
func NewDeleteOrderService(ctx context.Context, svcCtx *svccontext.ServiceContext) *DeleteOrderService {
	return &DeleteOrderService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteOrderService) DeleteOrder(req *types.GetOrderReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
