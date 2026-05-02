package taskservice

import (
	"context"

	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"
	types "github.com/dropship-dev/craftgo/example/internal/types/tasks"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// DeleteTaskService carries the per-request state for the DeleteTask
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type DeleteTaskService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDeleteTaskService constructs a fresh service instance bound to ctx.
func NewDeleteTaskService(ctx context.Context, svcCtx *svccontext.ServiceContext) *DeleteTaskService {
	return &DeleteTaskService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteTaskService) DeleteTask(req *types.GetTaskReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
