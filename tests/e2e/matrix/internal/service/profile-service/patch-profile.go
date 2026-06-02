package profileservice

import (
	"context"

	types "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/design"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// PatchProfileService carries the per-request state for the PatchProfile
// endpoint. The embedded log.Logger is pre-bound to the request
// context so logging surfaces trace_id / span_id / request_id.
type PatchProfileService struct {
	log.Logger
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewPatchProfileService constructs a fresh service instance bound to ctx.
func NewPatchProfileService(ctx context.Context, svcCtx *svccontext.ServiceContext) *PatchProfileService {
	return &PatchProfileService{
		Logger: log.Default().WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *PatchProfileService) PatchProfile(req *types.PatchProfileReq) (*types.PatchProfileResp, error) {
	return &types.PatchProfileResp{
		ID:             req.ID,
		DryRun:         req.DryRun,
		IdempotencyKey: req.IdempotencyKey,
		SessionToken:   req.SessionToken,
		DisplayName:    req.DisplayName,
	}, nil
}
