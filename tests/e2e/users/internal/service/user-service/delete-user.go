package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/users/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
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

func (l *DeleteUserService) DeleteUser() (*types.Empty, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	l.svcCtx.Users = map[string]map[string]any{}
	return &types.Empty{}, nil
}
