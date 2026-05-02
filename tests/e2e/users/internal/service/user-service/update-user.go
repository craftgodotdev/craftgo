package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/users/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
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
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row := map[string]any{"name": req.Name, "age": req.Age, "tags": req.Tags, "meta": req.Meta}
	l.svcCtx.Users[req.ID] = row
	return &types.User{ID: req.ID, Name: req.Name, Age: req.Age, Tags: req.Tags, Meta: req.Meta}, nil
}
