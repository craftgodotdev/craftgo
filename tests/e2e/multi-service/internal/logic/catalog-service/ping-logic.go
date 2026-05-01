// Package catalogservice carries the business logic for CatalogService.
// Counterpart to ordersservice — confirms both packages compile and route
// to their own handlers in one binary.
package catalogservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/multi-service/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/multi-service/svccontext"
)

// PingLogic carries per-request state for CatalogService.Ping.
type PingLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewPingLogic returns a fresh logic instance.
func NewPingLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *PingLogic {
	return &PingLogic{ctx: ctx, svcCtx: svcCtx}
}

// Ping returns a fixed Pong identifying the catalog service.
func (l *PingLogic) Ping() (*types.Pong, error) {
	return &types.Pong{Name: "catalog"}, nil
}
