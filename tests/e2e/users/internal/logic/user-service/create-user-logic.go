package userservice

import (
	"context"
	"fmt"

	types "github.com/dropship-dev/craftgo/tests/e2e/users/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
)

// CreateUserLogic carries per-request state for CreateUser.
type CreateUserLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateUserLogic returns a fresh logic instance.
func NewCreateUserLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateUserLogic {
	return &CreateUserLogic{ctx: ctx, svcCtx: svcCtx}
}

// CreateUser stores the request payload and returns the newly created user
// with a deterministic ID derived from the count of existing rows.
func (l *CreateUserLogic) CreateUser(req *types.CreateUserReq) (*types.User, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	id := fmt.Sprintf("u%d", len(l.svcCtx.Users)+1)
	row := map[string]any{"name": req.Name, "age": req.Age, "tags": req.Tags, "meta": req.Meta}
	l.svcCtx.Users[id] = row
	return &types.User{ID: id, Name: req.Name, Age: req.Age, Tags: req.Tags, Meta: req.Meta}, nil
}
