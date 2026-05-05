package userservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/users"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// ListSuspendedUsersService carries the per-request state for the ListSuspendedUsers
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type ListSuspendedUsersService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewListSuspendedUsersService constructs a fresh service instance bound to ctx.
func NewListSuspendedUsersService(ctx context.Context, svcCtx *svccontext.ServiceContext) *ListSuspendedUsersService {
	return &ListSuspendedUsersService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListSuspendedUsersService) ListSuspendedUsers(req *types.ListUsersReq) (*types.UserList, error) {
	// TODO: implement
	return nil, nil
}
