// Package adminservice carries the business logic for the auth-gated,
// /admin/-grouped service whose DSL lives in design/admin/. The
// generated handler/routes packages are emitted alongside this file by
// craftgo gen.
package adminservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
)

// DashboardStatsLogic carries per-request state for DashboardStats.
type DashboardStatsLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDashboardStatsLogic returns a fresh logic instance.
func NewDashboardStatsLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *DashboardStatsLogic {
	return &DashboardStatsLogic{ctx: ctx, svcCtx: svcCtx}
}

// DashboardStats returns the same shape ListProfilesResp uses, computed
// against the in-memory store. The auth middleware has already verified
// the bearer token by the time this runs.
func (l *DashboardStatsLogic) DashboardStats() (*types.ListProfilesResp, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	return &types.ListProfilesResp{Total: len(l.svcCtx.Profiles)}, nil
}
