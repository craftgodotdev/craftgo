package userservice

import (
	"context"

	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"
	types "github.com/dropship-dev/craftgo/example/internal/types/users"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// RestoreUserService carries the per-request state for the RestoreUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type RestoreUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewRestoreUserService constructs a fresh service instance bound to ctx.
func NewRestoreUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *RestoreUserService {
	return &RestoreUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *RestoreUserService) RestoreUser(req *types.GetUserReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
