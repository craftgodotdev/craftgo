package userservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/users"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// CreateUserService carries the per-request state for the CreateUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type CreateUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateUserService constructs a fresh service instance bound to ctx.
func NewCreateUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateUserService {
	return &CreateUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
	// TODO: implement
	return nil, nil
}
