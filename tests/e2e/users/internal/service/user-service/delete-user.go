package userservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/tests/e2e/users/internal/types/design"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/tests/e2e/users/svccontext"
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

func (l *DeleteUserService) DeleteUser(req *types.DeleteUserReq) (*types.Empty, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	delete(l.svcCtx.Users, req.ID)
	return &types.Empty{}, nil
}
