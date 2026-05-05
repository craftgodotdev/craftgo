package taskservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/tasks"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// ListTasksService carries the per-request state for the ListTasks
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type ListTasksService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewListTasksService constructs a fresh service instance bound to ctx.
func NewListTasksService(ctx context.Context, svcCtx *svccontext.ServiceContext) *ListTasksService {
	return &ListTasksService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListTasksService) ListTasks(req *types.ListTasksReq) (*types.TaskList, error) {
	// TODO: implement
	return nil, nil
}
