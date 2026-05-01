// Hand-written logic for the e2e suite. The codegen scaffolder skips this
// file because it already exists. Real projects fill these in by hand the
// same way.

package userservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/users/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
)

// GetUserLogic carries per-request state for GetUser.
type GetUserLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetUserLogic returns a fresh logic instance.
func NewGetUserLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetUserLogic {
	return &GetUserLogic{ctx: ctx, svcCtx: svcCtx}
}

// GetUser looks the user up in the in-memory store and returns it. A
// missing entry surfaces as a UserNotFound-shaped error.
func (l *GetUserLogic) GetUser(req *types.GetUserReq) (*types.User, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row, ok := l.svcCtx.Users[req.ID]
	if !ok {
		return nil, types.NewUserNotFoundErr()
	}
	return rowToUser(req.ID, row), nil
}

// rowToUser converts the dynamic in-memory store row to the typed wire
// representation. Kept here so callers don't repeat the conversion.
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

