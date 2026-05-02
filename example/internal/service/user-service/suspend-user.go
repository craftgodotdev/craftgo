package userservice

import (
	"context"

	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"
	types "github.com/dropship-dev/craftgo/example/internal/types/users"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// SuspendUserService carries the per-request state for the SuspendUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type SuspendUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewSuspendUserService constructs a fresh service instance bound to ctx.
func NewSuspendUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *SuspendUserService {
	return &SuspendUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *SuspendUserService) SuspendUser(req *types.GetUserReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
