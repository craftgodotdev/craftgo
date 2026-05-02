package userservice

import (
	"context"

	shared "github.com/dropship-dev/craftgo/example/internal/types/shared"
	types "github.com/dropship-dev/craftgo/example/internal/types/users"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/example/svccontext"
)

// AddContactService carries the per-request state for the AddContact
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type AddContactService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewAddContactService constructs a fresh service instance bound to ctx.
func NewAddContactService(ctx context.Context, svcCtx *svccontext.ServiceContext) *AddContactService {
	return &AddContactService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AddContactService) AddContact(req *types.AddContactReq) (*shared.OkResp, error) {
	// TODO: implement
	return nil, nil
}
