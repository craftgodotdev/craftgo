package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
)

// Which `@group` a service member belongs to. The group decides where a
// member's artefacts land and how a document tags it, so both answers come
// from here.

// methodGroupOf returns the @group of the block that declared m (primary or an
// extend), or "" when ungrouped / not found. The map-free form for callers
// that need one method's group without building the whole table.
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
				return EffectiveGroup(e, primaryGroup)
			}
		}
	}
	return ""
}

// EffectiveGroup returns the @group that applies to a service block -
// [route.EffectiveGroup] under codegen's own name. The block's own @group wins;
// an extend block that declares none inherits the primary block's.
func EffectiveGroup(block *ast.ServiceDecl, primaryGroup string) string {
	return route.EffectiveGroup(block, primaryGroup)
}
