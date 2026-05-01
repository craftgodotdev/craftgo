package profileservice

import (
	"context"
	"sort"

	types "github.com/dropship-dev/craftgo/tests/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
)

// AdminListProfilesLogic carries per-request state for AdminListProfiles.
// Identical responsibilities to ListProfilesLogic; the @middlewares
// decorator on the DSL method gates access at the routes layer so this
// function only ever sees authenticated callers.
type AdminListProfilesLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewAdminListProfilesLogic returns a fresh logic instance.
func NewAdminListProfilesLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *AdminListProfilesLogic {
	return &AdminListProfilesLogic{ctx: ctx, svcCtx: svcCtx}
}

// AdminListProfiles returns every stored profile. The auth middleware
// has already validated the bearer token by the time this runs.
func (l *AdminListProfilesLogic) AdminListProfiles() (*types.ListProfilesResp, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	ids := make([]string, 0, len(l.svcCtx.Profiles))
	for k := range l.svcCtx.Profiles {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	items := make([]types.Profile, 0, len(ids))
	for _, id := range ids {
		items = append(items, *l.svcCtx.Profiles[id].(*types.Profile))
	}
	return &types.ListProfilesResp{Items: items, Total: len(items)}, nil
}
