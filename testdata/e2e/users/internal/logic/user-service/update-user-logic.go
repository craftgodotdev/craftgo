package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/testdata/e2e/users/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/users/svccontext"
)

// UpdateUserLogic carries per-request state for UpdateUser.
type UpdateUserLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewUpdateUserLogic returns a fresh logic instance.
func NewUpdateUserLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *UpdateUserLogic {
	return &UpdateUserLogic{ctx: ctx, svcCtx: svcCtx}
}

// UpdateUser overwrites the row for the supplied request body. The handler
// hasn't bound the path :id yet (TODO in v1) so the request ID lives on
// req.Name placeholder until path-binding lands.
func (l *UpdateUserLogic) UpdateUser(req *types.CreateUserReq) (*types.User, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	id := req.Name
	row := map[string]any{"name": req.Name, "age": req.Age, "tags": req.Tags, "meta": req.Meta}
	l.svcCtx.Users[id] = row
	return &types.User{ID: id, Name: req.Name, Age: req.Age, Tags: req.Tags, Meta: req.Meta}, nil
}
