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
	return svc.blockGroup(block)
}

// blocks returns svc's declarations: the primary, then its extend blocks.
func (svc *ServiceInfo) blocks() []*ast.ServiceDecl {
	return append([]*ast.ServiceDecl{svc.Primary}, svc.Extends...)
}

// blockOf returns the declaration of svc that declares a method named like
// m, the primary before its extend blocks, or nil.
func (svc *ServiceInfo) blockOf(m *ast.Method) *ast.ServiceDecl {
	for _, b := range svc.blocks() {
		for _, bm := range b.Methods() {
			if bm.Name == m.Name {
				return b
			}
		}
	}
	return nil
}

// blockGroup returns the @group of b, a declaration of svc: its own, else
// the primary's.
func (svc *ServiceInfo) blockGroup(b *ast.ServiceDecl) string {
	return route.EffectiveGroup(b, route.ServiceGroup(svc.Primary))
}
