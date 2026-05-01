package userservice

import (
	"context"

	"github.com/dropship-dev/craftgo/tests/e2e/users/svccontext"
)

// PingLogic carries per-request state for the Ping endpoint.
type PingLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewPingLogic returns a fresh logic instance.
func NewPingLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *PingLogic {
	return &PingLogic{ctx: ctx, svcCtx: svcCtx}
}

// Ping is a liveness-style endpoint with no request and no response. It
// always succeeds; the handler responds 204 No Content.
func (l *PingLogic) Ping() error { return nil }
