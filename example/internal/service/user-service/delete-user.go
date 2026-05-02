package userservice

import (
	"context"

	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"
	types "github.com/dropship-dev/craftgo/example/internal/types/users"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// DeleteUserService carries the per-request state for the DeleteUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type DeleteUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDeleteUserService constructs a fresh service instance bound to ctx.
func NewDeleteUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *DeleteUserService {
	return &DeleteUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteUserService) DeleteUser(req *types.GetUserReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
