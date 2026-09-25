package semantic

import (
	"slices"

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

// InheritedDecorators returns the @middlewares, @security or @tags
// decorators, as name says, that apply to m, outermost first: service holds
// the primary service's, member those of m's extend block, then m's own. The
// method's own @ignoreMiddleware, @ignoreSecurity or @ignoreTags drops the
// inherited ones and sets ignored.
func (svc *ServiceInfo) InheritedDecorators(m *ast.Method, name string) (service, member []*ast.Decorator, ignored bool) {
	var inherited []*ast.Decorator
	if b := svc.blockOf(m); b != nil && b.Extend {
		inherited = inheritedFrom(b)
	}
	own := ownDecorators(m, inherited)
	ignored = ast.HasDecorator(own, chainIgnores[name])
	if !ignored {
		if svc.Primary != nil {
			service = decoratorsNamed(svc.Primary.Decorators, name)
		}
		member = decoratorsNamed(inherited, name)
	}
	return service, append(member, decoratorsNamed(own, name)...), ignored
}

// ownDecorators returns the decorators m declares itself: its Decorators
// without the inherited ones.
func ownDecorators(m *ast.Method, inherited []*ast.Decorator) []*ast.Decorator {
	return slices.DeleteFunc(slices.Clone(m.Decorators), func(d *ast.Decorator) bool {
		return d == nil || slices.Contains(inherited, d)
	})
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
