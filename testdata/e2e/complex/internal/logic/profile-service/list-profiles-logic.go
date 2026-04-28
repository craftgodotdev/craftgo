package profileservice

import (
	"context"
	"sort"

	types "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/svccontext"
)

// ListProfilesLogic carries per-request state for ListProfiles.
type ListProfilesLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewListProfilesLogic returns a fresh logic instance.
func NewListProfilesLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *ListProfilesLogic {
	return &ListProfilesLogic{ctx: ctx, svcCtx: svcCtx}
}

// ListProfiles returns every stored profile sorted by id for stable test
// assertions.
func (l *ListProfilesLogic) ListProfiles() (*types.ListProfilesResp, error) {
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
