package profileservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
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
// The error type's metadata is bound to the type — per-request context
// rides on the wrapping `fmt.Errorf` envelope instead.
func (l *GetProfileLogic) GetProfile(req *types.GetProfileReq) (*types.Profile, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row, ok := l.svcCtx.Profiles[req.ID]
	if !ok {
		return nil, types.NewProfileNotFoundErr()
	}
	return row.(*types.Profile), nil
}
