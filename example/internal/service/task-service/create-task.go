package taskservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/tasks"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// CreateTaskService carries the per-request state for the CreateTask
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type CreateTaskService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateTaskService constructs a fresh service instance bound to ctx.
func NewCreateTaskService(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateTaskService {
	return &CreateTaskService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateTaskService) CreateTask(req *types.CreateTaskReq) (*types.Task, error) {
	// TODO: implement
	return nil, nil
}
