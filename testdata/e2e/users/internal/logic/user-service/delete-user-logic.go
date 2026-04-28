package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/testdata/e2e/users/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/users/svccontext"
)

// DeleteUserLogic carries per-request state for DeleteUser.
type DeleteUserLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDeleteUserLogic returns a fresh logic instance.
func NewDeleteUserLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *DeleteUserLogic {
	return &DeleteUserLogic{ctx: ctx, svcCtx: svcCtx}
}

// DeleteUser removes every row in the in-memory store. With path-binding
// landed it would only delete the addressed row; for now the e2e test
// sequences calls so the wider blast radius is fine.
func (l *DeleteUserLogic) DeleteUser() (*types.Empty, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	l.svcCtx.Users = map[string]map[string]any{}
	return &types.Empty{}, nil
}
