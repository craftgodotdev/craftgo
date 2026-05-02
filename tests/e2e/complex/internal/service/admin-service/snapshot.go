package adminservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/complex/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
)

// SnapshotService carries the per-request state for the Snapshot
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type SnapshotService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewSnapshotService constructs a fresh service instance bound to ctx.
func NewSnapshotService(ctx context.Context, svcCtx *svccontext.ServiceContext) *SnapshotService {
	return &SnapshotService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *SnapshotService) Snapshot() (*types.ListProfilesResp, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	return &types.ListProfilesResp{Total: len(l.svcCtx.Profiles)}, nil
}
