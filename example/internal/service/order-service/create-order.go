package orderservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/example/internal/types/orders"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// CreateOrderService carries the per-request state for the CreateOrder
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type CreateOrderService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateOrderService constructs a fresh service instance bound to ctx.
func NewCreateOrderService(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateOrderService {
	return &CreateOrderService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateOrderService) CreateOrder(req *types.CreateOrderReq) (*types.Order, error) {
	// TODO: implement
	return nil, nil
}
