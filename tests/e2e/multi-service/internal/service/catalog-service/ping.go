package catalogservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/tests/e2e/multi-service/internal/types/design"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/tests/e2e/multi-service/svccontext"
)

// PingService carries the per-request state for the Ping
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type PingService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewPingService constructs a fresh service instance bound to ctx.
func NewPingService(ctx context.Context, svcCtx *svccontext.ServiceContext) *PingService {
	return &PingService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *PingService) Ping() (*types.Pong, error) {
	return &types.Pong{Name: "catalog"}, nil
}
