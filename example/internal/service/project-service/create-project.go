package projectservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/projects"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// CreateProjectService carries the per-request state for the CreateProject
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type CreateProjectService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateProjectService constructs a fresh service instance bound to ctx.
func NewCreateProjectService(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateProjectService {
	return &CreateProjectService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateProjectService) CreateProject(req *types.CreateProjectReq) (*types.Project, error) {
	// TODO: implement
	return nil, nil
}
