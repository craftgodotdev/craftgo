package profileservice

import (
	"context"

	"sort"
	types "github.com/dropship-dev/craftgo/tests/e2e/complex/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
)

// AdminListProfilesService carries the per-request state for the AdminListProfiles
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type AdminListProfilesService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewAdminListProfilesService constructs a fresh service instance bound to ctx.
func NewAdminListProfilesService(ctx context.Context, svcCtx *svccontext.ServiceContext) *AdminListProfilesService {
	return &AdminListProfilesService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AdminListProfilesService) AdminListProfiles() (*types.ListProfilesResp, error) {
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
