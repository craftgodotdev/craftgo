package catalogservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/multi-service/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/multi-service/svccontext"
)

// FeaturedService carries the per-request state for the Featured
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type FeaturedService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewFeaturedService constructs a fresh service instance bound to ctx.
func NewFeaturedService(ctx context.Context, svcCtx *svccontext.ServiceContext) *FeaturedService {
	return &FeaturedService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *FeaturedService) Featured() (*types.Item, error) {
	return &types.Item{Sku: l.svcCtx.ItemSKU, Price: l.svcCtx.ItemPrice}, nil
}
