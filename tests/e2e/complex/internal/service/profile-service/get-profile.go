package profileservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/tests/e2e/complex/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/complex/svccontext"
)

// GetProfileService carries the per-request state for the GetProfile
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type GetProfileService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetProfileService constructs a fresh service instance bound to ctx.
func NewGetProfileService(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetProfileService {
	return &GetProfileService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetProfileService) GetProfile(req *types.GetProfileReq) (*types.Profile, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	row, ok := l.svcCtx.Profiles[req.ID]
	if !ok {
		return nil, types.NewProfileNotFoundErr()
	}
	return row.(*types.Profile), nil
}
