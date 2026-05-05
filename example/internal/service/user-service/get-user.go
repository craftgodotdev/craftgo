package userservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/users"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// GetUserService carries the per-request state for the GetUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type GetUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetUserService constructs a fresh service instance bound to ctx.
func NewGetUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetUserService {
	return &GetUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetUserService) GetUser(req *types.GetUserReq) (*types.User, error) {
	// TODO: implement
	return nil, nil
}
