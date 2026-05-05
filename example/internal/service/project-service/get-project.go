package projectservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/projects"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// GetProjectService carries the per-request state for the GetProject
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type GetProjectService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewGetProjectService constructs a fresh service instance bound to ctx.
func NewGetProjectService(ctx context.Context, svcCtx *svccontext.ServiceContext) *GetProjectService {
	return &GetProjectService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetProjectService) GetProject(req *types.GetProjectReq) (*types.Project, error) {
	// TODO: implement
	return nil, nil
}
