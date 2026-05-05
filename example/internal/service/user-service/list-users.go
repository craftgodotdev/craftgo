package userservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/users"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// ListUsersService carries the per-request state for the ListUsers
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type ListUsersService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewListUsersService constructs a fresh service instance bound to ctx.
func NewListUsersService(ctx context.Context, svcCtx *svccontext.ServiceContext) *ListUsersService {
	return &ListUsersService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListUsersService) ListUsers(req *types.ListUsersReq) (*types.UserList, error) {
	// TODO: implement
	return nil, nil
}
