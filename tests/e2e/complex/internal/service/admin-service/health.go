package adminservice

import (
	"context"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/tests/e2e/complex/svccontext"
)

// HealthService carries the per-request state for the Health
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type HealthService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewHealthService constructs a fresh service instance bound to ctx.
func NewHealthService(ctx context.Context, svcCtx *svccontext.ServiceContext) *HealthService {
	return &HealthService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *HealthService) Health() error { return nil }
