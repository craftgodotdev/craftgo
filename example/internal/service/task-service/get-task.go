package taskservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/example/internal/types/tasks"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// GetTaskService carries the per-request state for the GetTask
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type GetTaskService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetTaskService constructs a fresh service instance bound to ctx.
func NewGetTaskService(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetTaskService {
	return &GetTaskService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetTaskService) GetTask(req *types.GetTaskReq) (*types.Task, error) {
	// TODO: implement
	return nil, nil
}
