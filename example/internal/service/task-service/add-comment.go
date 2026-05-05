package taskservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/example/internal/types/tasks"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// AddCommentService carries the per-request state for the AddComment
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type AddCommentService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewAddCommentService constructs a fresh service instance bound to ctx.
func NewAddCommentService(ctx context.Context, svcCtx *svccontext.ServiceContext) *AddCommentService {
	return &AddCommentService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AddCommentService) AddComment(req *types.AddCommentReq) (*types.Comment, error) {
	// TODO: implement
	return nil, nil
}
