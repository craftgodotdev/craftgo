package orderservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/orders"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// GetOrderService carries the per-request state for the GetOrder
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type GetOrderService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetOrderService constructs a fresh service instance bound to ctx.
func NewGetOrderService(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetOrderService {
	return &GetOrderService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetOrderService) GetOrder(req *types.GetOrderReq) (*types.Order, error) {
	// TODO: implement
	return nil, nil
}
