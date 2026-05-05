package adminservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/tests/e2e/complex/internal/types/design"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/tests/e2e/complex/svccontext"
)

// DashboardStatsService carries the per-request state for the DashboardStats
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type DashboardStatsService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDashboardStatsService constructs a fresh service instance bound to ctx.
func NewDashboardStatsService(ctx context.Context, svcCtx *svccontext.ServiceContext) *DashboardStatsService {
	return &DashboardStatsService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DashboardStatsService) DashboardStats() (*types.ListProfilesResp, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	return &types.ListProfilesResp{Total: len(l.svcCtx.Profiles)}, nil
}
