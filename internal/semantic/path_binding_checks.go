package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkAutoPathField checks each request field of m that auto-binds to a
// route variable.
func (a *analyzer) checkAutoPathField(m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	view, fields, ok := a.requestFields(m)
	if !ok {
		return
	}
	pathSegs := methodRoutePathVars(m, a.pkg.Services)
	if len(pathSegs) == 0 {
		return
	}
	reqName := m.Request.Name.String()
	for _, ff := range fields {
		a.autoPathFieldRule(reqName, view, pathSegs, ff.Field)
	}
}

// autoPathFieldRule rejects `?`, `@nullable`, `@default` or a non-path type on
// a field auto-bound to @path; the field's type resolves in package view.
func (a *analyzer) autoPathFieldRule(reqName, view string, pathSegs map[string]bool, f *ast.Field) {
	if f == nil || f.Type == nil {
		return
	}
	if b, auto := wire.RequestFieldBinding(f, pathSegs, false); b != wire.BindPath || !auto {
		return
	}
	switch {
	case f.Type.Optional:
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which a matched route always supplies - drop the optional `?` (a path parameter is never absent).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "nullable"):
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, but @nullable makes it a pointer while the path binder writes a plain string - drop @nullable (a path parameter has no null form).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "default"):
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which is always supplied, so @default can never apply - drop it.",
			reqName, f.Name, f.Name)
	case !a.pathBindableIn(view, f.Type):
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s auto-binds to the path segment {%s}, but @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
			reqName, f.Name, f.Name, f.Type.String())
	}
}

// checkDuplicatePathVars rejects a path variable repeated in m's route, the
// service @prefix included.
func (a *analyzer) checkDuplicatePathVars(svc *ast.ServiceDecl, m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	svcName := svc.Name
	// The registered route is the @prefix followed by the method path.
	seen := map[string]bool{}
	fromPrefix := map[string]bool{}
	for _, name := range route.Vars(route.ServicePrefix(svc)) {
		seen[name] = true
		fromPrefix[name] = true
	}
	for _, seg := range m.Path.Segments {
		if !seg.Param {
			continue
		}
		if seen[seg.Literal] {
			if fromPrefix[seg.Literal] {
				a.diag(seg.Pos, seg.Pos, lexer.SeverityError, CodeDuplicatePathVar,
					"%s.%s route repeats the path variable {%s} already bound by the service @prefix: the registered route is prefix + method path, so net/http's ServeMux panics on the duplicate wildcard at registration. Drop {%s} from the method path.",
					svcName, m.Name, seg.Literal, seg.Literal)
				return
			}
			a.diag(seg.Pos, seg.Pos, lexer.SeverityError, CodeDuplicatePathVar,
				"%s.%s route repeats the path variable {%s}: net/http's ServeMux panics on a duplicate wildcard at registration. Rename one segment.",
				svcName, m.Name, seg.Literal)
			return
		}
		seen[seg.Literal] = true
	}
}

// methodRoutePathVars returns the path variables of m's registered route,
// @prefix included; services is the package's service table.
func methodRoutePathVars(m *ast.Method, services map[string]*ServiceInfo) map[string]bool {
	vars := map[string]bool{}
	if m == nil {
		return vars
	}
	var owner *ast.ServiceDecl
	for _, si := range services {
		if si == nil {
			continue
		}
		for _, sm := range si.Methods {
			if sm == m {
				owner = si.Primary
			}
		}
	}
	for _, name := range route.Vars(route.Resolve("", owner, m)) {
		vars[name] = true
	}
	return vars
}
