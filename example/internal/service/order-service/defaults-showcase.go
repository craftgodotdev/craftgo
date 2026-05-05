package orderservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/orders"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// DefaultsShowcaseService carries the per-request state for the DefaultsShowcase
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type DefaultsShowcaseService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDefaultsShowcaseService constructs a fresh service instance bound to ctx.
func NewDefaultsShowcaseService(ctx context.Context, svcCtx *svccontext.ServiceContext) *DefaultsShowcaseService {
	return &DefaultsShowcaseService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DefaultsShowcaseService) DefaultsShowcase(req *types.DefaultsShowcaseReq) (*types.DefaultsShowcaseReq, error) {
	return req, nil
}
