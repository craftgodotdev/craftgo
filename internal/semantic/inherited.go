package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// chainIgnores maps each decorator a method inherits as a chain to the
// method decorator that drops the inherited part.
var chainIgnores = map[string]string{
	"middlewares": "ignoreMiddleware",
	"security":    "ignoreSecurity",
	"tags":        "ignoreTags",
}

// ExtendAllows reports whether decorator name may sit on an `extend service`
// block: @group, or a decorator every method of the block inherits.
func ExtendAllows(name string) bool {
	spec, ok := Lookup(name)
	return ok && (name == "group" || spec.Levels&LvlMethod != 0)
}

// inheritedFrom returns the decorators each method of extend block e
// inherits: every one [ExtendAllows] but @group.
func inheritedFrom(e *ast.ServiceDecl) []*ast.Decorator {
	var out []*ast.Decorator
	for _, d := range e.Decorators {
		if d.Name != "group" && ExtendAllows(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// Decorators returns the decorators that apply to m, a method of svc: those
// its extend block gives every method it declares, then m's own. The
// @middlewares, @security and @tags chains combine through
// [ServiceInfo.InheritedDecorators].
func (svc *ServiceInfo) Decorators(m *ast.Method) []*ast.Decorator {
	return blockMethodDecorators(svc.blockOf(m), m)
}

// blockMethodDecorators is [ServiceInfo.Decorators] for m, a method of block
// b (nil for none).
func blockMethodDecorators(b *ast.ServiceDecl, m *ast.Method) []*ast.Decorator {
	inherited := blockInherited(b)
	if len(inherited) == 0 {
		return m.Decorators
	}
	return append(inherited, m.Decorators...)
}

// blockInherited returns what block b gives each method it declares: nothing
// for a primary service or none, [inheritedFrom] for an extend block.
func blockInherited(b *ast.ServiceDecl) []*ast.Decorator {
	if b == nil || !b.Extend {
		return nil
	}
	return inheritedFrom(b)
}

// InheritedDecorators returns the @middlewares, @security or @tags
// decorators, as name says, that apply to m, outermost first: service holds
// the primary service's, member those of m's extend block, then m's own. The
// method's own @ignoreMiddleware, @ignoreSecurity or @ignoreTags drops the
// inherited ones and its extend block's drops the primary service's; either
// sets ignored.
func (svc *ServiceInfo) InheritedDecorators(m *ast.Method, name string) (service, member []*ast.Decorator, ignored bool) {
	inherited := blockInherited(svc.blockOf(m))
	ownIgnored := ast.HasDecorator(m.Decorators, chainIgnores[name])
	ignored = ownIgnored || ast.HasDecorator(inherited, chainIgnores[name])
	if !ignored && svc.Primary != nil {
		service = decoratorsNamed(svc.Primary.Decorators, name)
	}
	if !ownIgnored {
		member = decoratorsNamed(inherited, name)
	}
	return service, append(member, decoratorsNamed(m.Decorators, name)...), ignored
}

// decoratorsNamed returns the decorators of ds called name, in order.
func decoratorsNamed(ds []*ast.Decorator, name string) []*ast.Decorator {
	var out []*ast.Decorator
	for _, d := range ds {
		if d != nil && d.Name == name {
			out = append(out, d)
		}
	}
	return out
}
