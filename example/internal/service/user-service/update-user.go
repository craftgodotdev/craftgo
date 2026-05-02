package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/example/internal/types/users"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// UpdateUserService carries the per-request state for the UpdateUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type UpdateUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewUpdateUserService constructs a fresh service instance bound to ctx.
func NewUpdateUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *UpdateUserService {
	return &UpdateUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdateUserService) UpdateUser(req *types.UpdateUserReq) (*types.User, error) {
	// TODO: implement
	return nil, nil
}
