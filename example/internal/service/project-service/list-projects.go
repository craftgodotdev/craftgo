package projectservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/projects"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// ListProjectsService carries the per-request state for the ListProjects
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type ListProjectsService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewListProjectsService constructs a fresh service instance bound to ctx.
func NewListProjectsService(ctx context.Context, svcCtx *svccontext.ServiceContext) *ListProjectsService {
	return &ListProjectsService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListProjectsService) ListProjects(req *types.ListProjectsReq) (*types.ProjectList, error) {
	// TODO: implement
	return nil, nil
}
