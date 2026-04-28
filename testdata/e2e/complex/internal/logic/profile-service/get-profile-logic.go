package profileservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/svccontext"
)

// GetProfileLogic carries per-request state for GetProfile.
type GetProfileLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetProfileLogic returns a fresh logic instance.
func NewGetProfileLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetProfileLogic {
	return &GetProfileLogic{ctx: ctx, svcCtx: svcCtx}
}

// GetProfile looks the addressed profile up. A miss surfaces as
// ProfileNotFound which the framework maps to HTTP 404 automatically.
// We use `.WithMessage(...)` to inject a per-request friendly message
// while keeping the canonical Code (`PROFILE_NOT_FOUND`) intact.
func (l *GetProfileLogic) GetProfile(req *types.GetProfileReq) (*types.Profile, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row, ok := l.svcCtx.Profiles[req.ID]
	if !ok {
		return nil, types.NewProfileNotFoundErr().
			WithMessage("Profile " + req.ID + " does not exist")
	}
	return row.(*types.Profile), nil
}
