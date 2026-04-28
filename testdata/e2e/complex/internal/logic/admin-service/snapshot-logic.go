package adminservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/svccontext"
)

// SnapshotLogic carries per-request state for AdminService.Snapshot.
type SnapshotLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewSnapshotLogic returns a fresh logic instance.
func NewSnapshotLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *SnapshotLogic {
	return &SnapshotLogic{ctx: ctx, svcCtx: svcCtx}
}

// Snapshot returns the same payload as DashboardStats. Both middlewares
// (AuthRequired + RequestStamp) have already run by the time this fires.
func (l *SnapshotLogic) Snapshot() (*types.ListProfilesResp, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	return &types.ListProfilesResp{Total: len(l.svcCtx.Profiles)}, nil
}
