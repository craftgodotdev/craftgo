package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// MethodGroupOf returns the @group of the block that declares m (an extend
// block without one inherits the primary's), or "" when none applies.
func MethodGroupOf(svc *ServiceInfo, m *ast.Method) string {
	if svc == nil || m == nil {
		return ""
	}
	block := svc.blockOf(m)
	if block == nil {
		return ""
	}
	return route.EffectiveGroup(block, route.ServiceGroup(svc.Primary))
}
