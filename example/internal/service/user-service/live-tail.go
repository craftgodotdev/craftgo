package userservice

import (
	"context"

	"net/http"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// LiveTailService carries the per-request state for the LiveTail
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type LiveTailService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewLiveTailService constructs a fresh service instance bound to ctx.
func NewLiveTailService(ctx context.Context, svcCtx *svccontext.ServiceContext) *LiveTailService {
	return &LiveTailService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *LiveTailService) LiveTail(w http.ResponseWriter, r *http.Request) error {
	// TODO: implement
	http.Error(w, "not implemented", http.StatusNotImplemented)
	return nil
}
