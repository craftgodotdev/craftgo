package ordersservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/multi-service/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/multi-service/svccontext"
)

// LatestService carries the per-request state for the Latest
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type LatestService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewLatestService constructs a fresh service instance bound to ctx.
func NewLatestService(ctx context.Context, svcCtx *svccontext.ServiceContext) *LatestService {
	return &LatestService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *LatestService) Latest() (*types.Order, error) {
	return &types.Order{ID: l.svcCtx.OrderID, Total: l.svcCtx.OrderTotal}, nil
}
