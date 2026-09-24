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
	primaryGroup := route.ServiceGroup(svc.Primary)
	if svc.Primary != nil {
		for _, pm := range svc.Primary.Methods() {
			if pm.Name == m.Name {
				return primaryGroup
			}
		}
	}
	for _, e := range svc.Extends {
		for _, em := range e.Methods() {
			if em.Name == m.Name {
				return route.EffectiveGroup(e, primaryGroup)
			}
		}
	}
	return ""
}
