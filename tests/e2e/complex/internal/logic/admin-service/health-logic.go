package adminservice

import (
	"context"

	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
)

// HealthLogic carries per-request state for the AdminService.Health
// liveness endpoint. The auth middleware still applies — even health
// requires authentication on the admin surface.
type HealthLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewHealthLogic returns a fresh logic instance.
func NewHealthLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *HealthLogic {
	return &HealthLogic{ctx: ctx, svcCtx: svcCtx}
}

// Health is a no-op probe. Returns nil so the handler responds 204.
func (l *HealthLogic) Health() error { return nil }
