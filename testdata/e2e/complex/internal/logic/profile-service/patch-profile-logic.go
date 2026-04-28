package profileservice

import (
	"context"

	types "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/svccontext"
)

// PatchProfileLogic carries per-request state for PatchProfile. It is
// the every-binding-kind demo: the test asserts that path / query /
// header / cookie / body all reach this function via the request type.
type PatchProfileLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewPatchProfileLogic returns a fresh logic instance.
func NewPatchProfileLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *PatchProfileLogic {
	return &PatchProfileLogic{ctx: ctx, svcCtx: svcCtx}
}

// PatchProfile echoes every bound value back so the e2e test can assert
// roundtrip per binding kind. No actual store mutation — keep the test
// focused on the binding mechanics.
func (l *PatchProfileLogic) PatchProfile(req *types.PatchProfileReq) (*types.PatchProfileResp, error) {
	return &types.PatchProfileResp{
		ID:             req.ID,
		DryRun:         req.DryRun,
		IdempotencyKey: req.IdempotencyKey,
		SessionToken:   req.SessionToken,
		DisplayName:    req.DisplayName,
	}, nil
}
