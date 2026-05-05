package orderservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/orders"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// ListOrdersService carries the per-request state for the ListOrders
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type ListOrdersService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewListOrdersService constructs a fresh service instance bound to ctx.
func NewListOrdersService(ctx context.Context, svcCtx *svccontext.ServiceContext) *ListOrdersService {
	return &ListOrdersService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListOrdersService) ListOrders(req *types.ListOrdersReq) (*types.OrderList, error) {
	// TODO: implement
	return nil, nil
}
