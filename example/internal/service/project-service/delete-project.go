package projectservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/example/internal/types/projects"
	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// DeleteProjectService carries the per-request state for the DeleteProject
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type DeleteProjectService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewDeleteProjectService constructs a fresh service instance bound to ctx.
func NewDeleteProjectService(ctx context.Context, svcCtx *svccontext.ServiceContext) *DeleteProjectService {
	return &DeleteProjectService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteProjectService) DeleteProject(req *types.GetProjectReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
