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

// UpdateUser overwrites the row addressed by the path-bound id with the
// fields carried in the request body.
func (l *UpdateUserLogic) UpdateUser(req *types.UpdateUserReq) (*types.User, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row := map[string]any{"name": req.Name, "age": req.Age, "tags": req.Tags, "meta": req.Meta}
	l.svcCtx.Users[req.ID] = row
	return &types.User{ID: req.ID, Name: req.Name, Age: req.Age, Tags: req.Tags, Meta: req.Meta}, nil
}
