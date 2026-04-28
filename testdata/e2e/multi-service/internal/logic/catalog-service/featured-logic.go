package catalogservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/testdata/e2e/multi-service/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/multi-service/svccontext"
)

// FeaturedLogic carries per-request state for CatalogService.Featured.
type FeaturedLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewFeaturedLogic returns a fresh logic instance.
func NewFeaturedLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *FeaturedLogic {
	return &FeaturedLogic{ctx: ctx, svcCtx: svcCtx}
}

// Featured reads the seeded hero item from svcCtx.
func (l *FeaturedLogic) Featured() (*types.Item, error) {
	return &types.Item{Sku: l.svcCtx.ItemSKU, Price: l.svcCtx.ItemPrice}, nil
}
