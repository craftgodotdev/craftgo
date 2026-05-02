package projectservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/example/internal/types/projects"
	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// AddMemberService carries the per-request state for the AddMember
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type AddMemberService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewAddMemberService constructs a fresh service instance bound to ctx.
func NewAddMemberService(ctx context.Context, svcCtx *svccontext.ServiceContext) *AddMemberService {
	return &AddMemberService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AddMemberService) AddMember(req *types.AddMemberReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
