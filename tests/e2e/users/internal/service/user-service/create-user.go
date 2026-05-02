package userservice

import (
	"context"

	"fmt"
	types "github.com/dropship-dev/craftgo/tests/e2e/users/internal/types/design"

	"github.com/dropship-dev/craftgo/pkg/log"
	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
)

// CreateUserService carries the per-request state for the CreateUser
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type CreateUserService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateUserService constructs a fresh service instance bound to ctx.
func NewCreateUserService(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateUserService {
	return &CreateUserService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateUserService) CreateUser(req *types.CreateUserReq) (*types.User, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()
	id := fmt.Sprintf("u%d", len(l.svcCtx.Users)+1)
	row := map[string]any{"name": req.Name, "age": req.Age, "tags": req.Tags, "meta": req.Meta}
	l.svcCtx.Users[id] = row
	return &types.User{ID: id, Name: req.Name, Age: req.Age, Tags: req.Tags, Meta: req.Meta}, nil
}
