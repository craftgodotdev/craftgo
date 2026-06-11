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
// plain string into it (`req.ID = r.PathValue(...)` into a `*string` —
// non-compiling); and `@default` can never apply to an always-present
// segment. The explicit `@path` form is already rejected for these; this
// mirrors it for the implicit auto-@path path, on every verb.
func (a *analyzer) checkAutoPathField(m *ast.Method) {
	if m == nil || m.Request == nil || m.Path == nil {
		return
	}
	td, ok := a.pkg.Types[m.Request.Name.String()]
	if !ok {
		return // cross-package request — handled by checkProjectAutoPathField
	}
	pathSegs := MethodRoutePathVars(m, a.pkg.Services)
	if len(pathSegs) == 0 {
		return
	}
	reqName := m.Request.Name.String()
	emit := func(pos lexer.Position, code, format string, args ...any) {
		a.diag(pos, pos, lexer.SeverityError, code, format, args...)
	}
	// Resolve bindability against the local table; a qualified cross-package
	// type is deferred to the project twin, which sees the foreign package.
	unbindable := func(f *ast.Field) bool {
		return !isQualifiedTypeRef(f.Type) && !isPathBindingType(f.Type, a.pkg)
	}
	for _, f := range a.flattenRequestFields(td.Body, map[string]bool{}) {
		autoPathFieldRule(reqName, pathSegs, f, unbindable, emit)
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
// string into — non-compiling), and a non-bindable field type is rejected
// when resolvable. localPkg resolves an unqualified field type's
// path-bindability; pass nil (project pass) or a qualified type to DEFER the
// type check to codegen. Shared by the per-package and project passes so the
// explicit/auto and local/cross-package forms all agree.
func autoPathFieldRule(reqName string, pathSegs map[string]bool, f *ast.Field, typeUnbindable func(*ast.Field) bool, emit func(pos lexer.Position, code, format string, args ...any)) {
	if f == nil || f.Type == nil {
		return
	}
	if kind, auto := wire.RequestFieldBinding(f, pathSegs, false); kind != wire.BindingPath || !auto {
		return
	}
	switch {
	case f.Type.Optional:
		emit(f.Pos, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which a matched route always supplies — drop the optional `?` (a path parameter is never absent).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "nullable"):
		emit(f.Pos, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, but @nullable makes it a pointer while the path binder writes a plain string — drop @nullable (a path parameter has no null form).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "default"):
		emit(f.Pos, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which is always supplied, so @default can never apply — drop it.",
			reqName, f.Name, f.Name)
	case typeUnbindable != nil && typeUnbindable(f):
		// A path segment carries a single primitive/scalar/enum value; a
		// struct / map / array / generic field that auto-binds to it has no
		// wire form. The caller decides bindability — the per-package pass
		// against its local table (deferring qualified cross-package refs to
		// the project twin), the project twin against the resolved IR (which
		// sees cross-package scalars / enums a local table can't).
		emit(f.Pos, CodeBindingType,
			"field %s.%s auto-binds to the path segment {%s}, but @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
			reqName, f.Name, f.Name, describeTypeRef(f.Type))
	}
}

// pathBindableIR reports whether a resolved field can source a path segment —
// a single wire-string value: a wire primitive (string/bool/int*/uint*/
// float*), a scalar wrapping one, or an enum. Structs, maps, arrays, bytes,
// any, and file have no path-string form. The optional / array shapes are
// rejected by the structural arms of [autoPathFieldRule] before this runs.
// This is the cross-package twin of [isPathBindingType]: it resolves through
// the IR so a `lib.Scalar` / `lib.Enum` is judged by what it wraps, not
// false-rejected for being unresolvable in the using package's local table.
func pathBindableIR(rf ResolvedField) bool {
	switch rf.Category {
	case CatPrimitive, CatEnum:
		return true
	case CatScalar:
		return isPrimitiveWireName(rf.ResolvedPrim)
	}
	return false
}

// wireBindableIR reports whether a field can ride a @query string. It is the
// cross-package twin of [isWireBindingType]: like a path value the element
// must be a wire primitive / scalar-over-one / enum, but a query also accepts
// a 1-D array (repeated values, `?x=1&x=2`). Maps, generics, and nested
// arrays have no wire form. The element is resolved through the IR so a
// cross-package `lib.Scalar` / `lib.Enum` is judged by what it wraps.
func wireBindableIR(f *ast.Field, proj *Project) bool {
	t := f.Type
	if t == nil || t.Map != nil || t.Named == nil || len(t.Named.Args) > 0 || t.ArrayDepth > 1 {
		return false
	}
	// An array rides as repeated single values, so judge the element type.
	elem := *f
	et := *t
	et.Array = false
	et.ArrayDepth = 0
	elem.Type = &et
	return pathBindableIR(ResolveField(&elem, nil, proj))
}

// checkProjectAutoPathField is the cross-package twin of checkAutoPathField:
// the per-package pass returns early for a QUALIFIED request type
// (`request shared.R`), so without this an auto-path field carrying
// `@nullable` (non-compiling) / `?` / `@default` on a cross-package request
// silently slips through. Only qualified requests are processed here (local
// ones are already covered, and re-checking would double-report). The
// type-bindability arm is deferred (localPkg=nil) — the structural decorator
// checks (the #16 non-compile) need no type resolution.
func (r *refResolver) checkProjectAutoPathField() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				if m == nil || m.Request == nil || m.Request.Name == nil || m.Path == nil {
					continue
				}
				parts := m.Request.Name.Parts
				if len(parts) != 2 {
					continue // local request — per-package pass owns it
				}
				home := r.proj.Packages[parts[0]]
				if home == nil {
					continue
				}
				td, ok := home.Types[parts[1]]
				if !ok {
					continue
				}
				pathSegs := MethodRoutePathVars(m, pkg.Services)
				if len(pathSegs) == 0 {
					continue
				}
				fields := map[string]*ast.Field{}
				r.collectGroupFieldsProject(parts[0], td.Body, fields, map[string]bool{})
				reqName := m.Request.Name.String()
				emit := func(pos lexer.Position, code, format string, args ...any) {
					r.diag(pos, lexer.SeverityError, code, format, args...)
				}
				// The IR resolves a cross-package field's type (collectGroupFields
				// Project requalified each promoted field to its home package), so
				// a foreign struct / array / map that auto-binds to a path segment
				// is caught here — the gap the per-package pass defers.
				unbindable := func(f *ast.Field) bool {
					return !pathBindableIR(ResolveField(f, nil, r.proj))
				}
				for _, f := range fields {
					autoPathFieldRule(reqName, pathSegs, f, unbindable, emit)
				}
			}
		}
	}
}

// checkDuplicatePathVars rejects a route template that repeats a path
// variable name (`/items/{id}/x/{id}`). net/http's ServeMux panics at
// registration on a duplicate wildcard, so gen would produce a server
// that crashes on boot — caught here at design time instead.
func (a *analyzer) checkDuplicatePathVars(svc *ast.ServiceDecl, m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	svcName := svc.Name
	// Seed with the service @prefix's path variables. The registered route is
	// prefix + method path (see resolveRoute), so a method segment that reuses
	// a prefix variable produces a duplicate wildcard in the combined route
	// exactly as a method-internal repeat does — and ServeMux panics on it at
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
// registered route — its owning service's @prefix variables PLUS the method
// path variables. The auto-binding rule ([RequestFieldBinding]) and the
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

// flattenRequestFields returns body's fields with embedded same-package
// mixins expanded recursively, mirroring the codegen request flatten so
// the method-level binding checks see a field a request inherits through a
// mixin. A qualified (cross-package) mixin is skipped here and left to the
// project resolver, matching the per-package analyzer's scope. `seen`
// breaks mixin cycles. Generic-argument substitution is not modelled — the
// binding checks key on the field's decorators and shape, which a generic
// mixin's promoted field carries regardless of the concrete argument.
func (a *analyzer) flattenRequestFields(body []ast.TypeMember, seen map[string]bool) []*ast.Field {
	var out []*ast.Field
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			out = append(out, v)
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil || len(v.Ref.Name.Parts) != 1 {
				continue
			}
			name := v.Ref.Name.Parts[0]
			if seen[name] {
				continue
			}
			seen[name] = true
			if td, ok := a.pkg.Types[name]; ok {
				out = append(out, a.flattenRequestFields(td.Body, seen)...)
			}
		}
	}
	return out
}
