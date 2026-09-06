// Path-binding checks: auto-@path promotion, duplicate path variables
// (method-local and @prefix-crossing), and the full-route path-variable set
// the request auto-binding rule reads.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkAutoPathField rejects optional (`?`) / `@nullable` / `@default` on a
// request field that auto-binds to a `{param}` segment (its name matches the
// segment and it carries no explicit binding decorator). A matched route
// always supplies the segment, so an optional path field is meaningless;
// `@nullable` lowers the field to a pointer while the path binder writes a
// plain string into it (`req.ID = r.PathValue(...)` into a `*string` -
// non-compiling); and `@default` can never apply to an always-present
// segment. The explicit `@path` form is already rejected for these; this
// mirrors it for the implicit auto-@path path, on every verb.
func (a *analyzer) checkAutoPathField(m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	td, fields := a.requestFields(m)
	if td == nil {
		return
	}
	pathSegs := MethodRoutePathVars(m, a.pkg.Services)
	if len(pathSegs) == 0 {
		return
	}
	reqName := m.Request.Name.String()
	for _, pf := range fields {
		a.autoPathFieldRule(reqName, pathSegs, pf)
	}
}

// pathSegments returns the set of `{param}` segment names in a method's
// route.
func pathSegments(m *ast.Method) map[string]bool {
	out := map[string]bool{}
	if m == nil || m.Path == nil {
		return out
	}
	for _, seg := range m.Path.Segments {
		if seg.Param {
			out[seg.Literal] = true
		}
	}
	return out
}

// autoPathFieldRule checks one request field that auto-binds to a path
// segment (its name matches a `{param}` and it carries no explicit binding
// decorator): optional `?` / `@nullable` / `@default` are rejected (a matched
// route always supplies the segment, with no optional / null / default form,
// and `@nullable` lowers to a pointer the path binder can't write a plain
// string into - non-compiling), and a type that cannot source a path
// segment is rejected. The field's type resolves in the package that
// declares it, so a field promoted from a foreign mixin is judged there.
func (a *analyzer) autoPathFieldRule(reqName string, pathSegs map[string]bool, pf promotedField) {
	f := pf.Field
	if f == nil || f.Type == nil {
		return
	}
	if kind, auto := wire.RequestFieldBinding(f, pathSegs, false); kind != wire.BindingPath || !auto {
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
	case !a.pathBindableIn(pf.Pkg, f.Type):
		// A path segment carries a single primitive/scalar/enum value; a
		// struct / map / array / generic field that auto-binds to it has no
		// wire form.
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s auto-binds to the path segment {%s}, but @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
			reqName, f.Name, f.Name, describeTypeRef(f.Type))
	}
}

// checkDuplicatePathVars rejects a route template that repeats a path
// variable name (`/items/{id}/x/{id}`). net/http's ServeMux panics at
// registration on a duplicate wildcard, so gen would produce a server
// that crashes on boot - caught here at design time instead.
func (a *analyzer) checkDuplicatePathVars(svc *ast.ServiceDecl, m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	svcName := svc.Name
	// Seed with the service @prefix's path variables. The registered route is
	// prefix + method path (see resolveRoute), so a method segment that reuses
	// a prefix variable produces a duplicate wildcard in the combined route
	// exactly as a method-internal repeat does - and ServeMux panics on it at
	// boot all the same.
	seen := map[string]bool{}
	fromPrefix := map[string]bool{}
	for _, name := range prefixPathVars(svc) {
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

// prefixPathVars returns the `{name}` path-variable names declared in a
// service's @prefix (e.g. @prefix("/tenant/{tenantID}") → ["tenantID"]).
// Empty when the service is nil, has no prefix, or no variable segments.
func prefixPathVars(svc *ast.ServiceDecl) []string {
	if svc == nil {
		return nil
	}
	p := route.ServicePrefix(svc)
	if p == "" {
		return nil
	}
	var out []string
	for seg := range strings.SplitSeq(p, "/") {
		if len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, seg[1:len(seg)-1])
		}
	}
	return out
}

// MethodRoutePathVars returns the path-variable names in method m's full
// registered route - its owning service's @prefix variables PLUS the method
// path variables. The auto-binding rule ([wire.RequestFieldBinding]) and the
// auto-@path / body-verb checks read this rather than the method path alone,
// so they agree with the route that actually registers: a field whose name
// matches a @prefix variable auto-binds to @path exactly like one matching a
// method-path variable (without it, the field would wrongly fall through to
// @query on a GET or @body on a POST, and the path value would never bind).
// services is the analysed package's service table (pkg.Services), used to
// find m's owning service for its prefix.
func MethodRoutePathVars(m *ast.Method, services map[string]*ServiceInfo) map[string]bool {
	vars := pathSegments(m)
	for _, si := range services {
		if si == nil {
			continue
		}
		for _, sm := range si.Methods {
			if sm == m {
				for _, name := range prefixPathVars(si.Primary) {
					vars[name] = true
				}
				return vars
			}
		}
	}
	return vars
}
