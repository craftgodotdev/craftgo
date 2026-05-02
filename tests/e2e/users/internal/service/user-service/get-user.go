package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/users/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
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
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row, ok := l.svcCtx.Users[req.ID]
	if !ok {
		return nil, types.NewUserNotFoundErr()
	}
	return rowToUser(req.ID, row), nil
}
func rowToUser(id string, row map[string]any) *types.User {
	u := &types.User{ID: id}
	if v, ok := row["name"].(string); ok {
		u.Name = v
	}
	if v, ok := row["age"].(*int); ok {
		u.Age = v
	}
	if v, ok := row["tags"].([]string); ok {
		u.Tags = v
	}
	if v, ok := row["meta"].(map[string]string); ok {
		u.Meta = v
	}
	return u
}
