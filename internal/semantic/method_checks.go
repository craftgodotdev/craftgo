// Method-level combination checks: request body type, body-verb rules,
// @status(204) bodies, and @passthrough constraints.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkRequestBodyType rejects a request type that is a bare scalar or enum
// (a fieldless named type). The request binder/decoder drives off the
// type's FIELDS, so a fieldless type yields no decode and no parameters —
// the client payload is silently dropped (and a constraint-free scalar
// produces non-compiling Go, since the handler calls a Validate() that
// isn't generated). Wrap the value in a `type { value <T> }`. Mirrors the
// existing bare-array request reject. Only local (unqualified) request
// types are resolved here; a qualified cross-package scalar/enum request is
// rare and left to codegen.
func (a *analyzer) checkRequestBodyType(m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil || len(m.Request.Name.Parts) != 1 {
		return
	}
	name := m.Request.Name.String()
	kind := bareRequestKind(a.pkg, name)
	if kind == "" {
		return
	}
	a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodeBindingType,
		"request type %q is a %s, which has no fields to bind or decode as a request body — wrap it in a type (`type Req { value %s }`)",
		name, kind, name)
}

// bareRequestKind reports whether `name` resolves to a scalar or enum in pkg
// (a fieldless type that has nothing to bind or decode as a request body), or
// "" otherwise. Shared by the per-package and project request-type checks.
func bareRequestKind(pkg *Package, name string) string {
	if pkg == nil {
		return ""
	}
	if _, ok := pkg.Scalars[name]; ok {
		return "scalar"
	}
	if _, ok := pkg.Enums[name]; ok {
		return "enum"
	}
	return ""
}

// checkProjectRequestBodyType is the cross-package twin of
// checkRequestBodyType: a qualified `request shared.Email` whose target is a
// scalar/enum in the sibling package is rejected (the per-package pass only
// resolves a 1-part local name).
func (r *refResolver) checkProjectRequestBodyType() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				if m == nil || m.Request == nil || m.Request.Name == nil || len(m.Request.Name.Parts) != 2 {
					continue
				}
				parts := m.Request.Name.Parts
				kind := bareRequestKind(r.proj.Packages[parts[0]], parts[1])
				if kind == "" {
					continue
				}
				name := m.Request.Name.String()
				r.diag(m.Request.Pos, lexer.SeverityError, CodeBindingType,
					"request type %q is a %s, which has no fields to bind or decode as a request body — wrap it in a type (`type Req { value %s }`)",
					name, kind, name)
			}
		}
	}
}

// checkNoContentStatusBody rejects a no-content success status (204, 304,
// or any 1xx) on a method that declares a response body. Per RFC 9110
// those statuses carry no body, but both the OpenAPI emitter and the
// transport template select their body-emitting branch on response-body
// presence alone — never the status — so the pairing would advertise a
// `application/json` body under a status that forbids one and write a body
// the client never receives.
func (a *analyzer) checkNoContentStatusBody(m *ast.Method) {
	if m == nil || m.Response == nil || m.Response.Type == nil {
		return
	}
	for _, d := range m.Decorators {
		if d == nil || d.Name != "status" || len(d.Args) != 1 {
			continue
		}
		il, ok := d.Args[0].Value.(*ast.IntLit)
		if !ok {
			continue
		}
		code := il.Value
		if code == 204 || code == 205 || code == 304 || (code >= 100 && code < 200) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
				"@status(%d) is a no-content status and cannot carry a response body, but method %s declares one — drop the response, or use a status that allows a body.",
				code, m.Name)
			return
		}
	}
}

// checkPassthroughBody rejects `request` or `response` blocks on any
// method tagged `@passthrough`. The decorator hands the raw
// http.ResponseWriter and *http.Request to logic; declaring a typed
// shape next to it would mislead readers into expecting framework
// validation that never runs.
func (a *analyzer) checkPassthroughBody(svcName string, m *ast.Method) {
	var passPos lexer.Position
	hasPassthrough := false
	for _, d := range m.Decorators {
		if d == nil {
			continue
		}
		if d.Name == "passthrough" {
			hasPassthrough = true
			passPos = d.Pos
			break
		}
	}
	if !hasPassthrough {
		return
	}
	if m.Request != nil {
		diag := a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodePassthroughBody,
			"method %s.%s: @passthrough method must not declare request or response - logic handles wire format directly",
			svcName, m.Name)
		diag.Related = related(passPos, "@passthrough declared here")
	}
	if m.Response != nil {
		pos := m.Response.Pos
		if m.Response.Type != nil {
			pos = m.Response.Type.Pos
		}
		diag := a.diag(pos, pos, lexer.SeverityError, CodePassthroughBody,
			"method %s.%s: @passthrough method must not declare request or response - logic handles wire format directly",
			svcName, m.Name)
		diag.Related = related(passPos, "@passthrough declared here")
	}
}

// checkBodyBindingVerb rejects `@body` / `@form` request fields on a
// non-body verb (GET / HEAD / DELETE / OPTIONS). Those handlers never
// decode a request body, so the binder's switch falls through and the
// field is left zero with no error — silent data loss. The OpenAPI side
// likewise omits the requestBody for non-body verbs, so the contract and
// the runtime agree only by both dropping the field. Reject up front.
//
// Resolves the request type from the local package; a cross-package
// request DTO (rare) is left to the codegen pass. Body verbs route
// `@body` through the JSON decoder and `@form` through the multipart
// handler, so the check only fires for the non-body set.
func (a *analyzer) checkBodyBindingVerb(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil {
		return
	}
	switch strings.ToUpper(m.Verb) {
	case "POST", "PUT", "PATCH":
		return // body-bearing verbs decode @body / @form normally
	}
	td, ok := a.pkg.Types[m.Request.Name.String()]
	if !ok {
		return
	}
	// Flatten so a field a request inherits through a mixin is checked too:
	// without this an auto-@query non-bindable field (or a @body / @form
	// field) promoted via a mixin slips past the semantic gate and fails
	// only at the codegen stage with a position-less error the LSP can't
	// surface. Mirrors the codegen request flatten.
	verb := strings.ToUpper(m.Verb)
	reqName := m.Request.Name.String()
	pathSegs := MethodRoutePathVars(m, a.pkg.Services)
	emit := func(start, end lexer.Position, code, format string, args ...any) {
		a.diag(start, end, lexer.SeverityError, code, format, args...)
	}
	unbindable := func(f *ast.Field) bool {
		return !isQualifiedTypeRef(f.Type) && !isWireBindingType(f.Type, a.pkg)
	}
	for _, f := range a.flattenRequestFields(td.Body, map[string]bool{}) {
		bodyBindingVerbRules(reqName, verb, svcName, pathSegs, f, unbindable, emit)
	}
}

// bodyBindingVerbRules checks one request field of a NON-body-verb method:
// `@body` / `@form` require a body-bearing verb (the handler decodes no body,
// so the field would be silently dropped); an un-decorated field auto-binds
// to @query, where `@nullable` is meaningless (a query string has no
// JSON-null form, and the pointer it lowers to can't take the binder's plain
// string — non-compiling); and a non-bindable auto-@query type is rejected
// when resolvable. The first two are STRUCTURAL (no type resolution) and fire
// for cross-package fields too; the type check is delegated to typeUnbindable
// (the per-package pass resolves against its local table and defers qualified
// cross-package refs, the project twin resolves through the IR). Shared by the
// per-package and project passes.
func bodyBindingVerbRules(reqName, verb, svcName string, pathSegs map[string]bool, f *ast.Field, typeUnbindable func(*ast.Field) bool, emit func(start, end lexer.Position, code, format string, args ...any)) {
	if f == nil {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || (d.Name != wire.BindingBody && d.Name != wire.BindingForm) {
			continue
		}
		emit(d.Pos, decoratorEnd(d), CodeBindingVerb,
			"field %s.%s: @%s requires a body-bearing verb (POST/PUT/PATCH) — the %s %s handler decodes no request body, so the field would be silently dropped",
			reqName, f.Name, d.Name, verb, svcName)
		break // one diagnostic per field
	}
	if f.Type == nil {
		return
	}
	if kind, auto := wire.RequestFieldBinding(f, pathSegs, false); kind != wire.BindingQuery || !auto {
		return
	}
	if ast.HasDecorator(f.Decorators, "nullable") {
		emit(f.Pos, f.Pos, CodeDecoratorConflict,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but @nullable has no meaning on a wire parameter — a query string has no JSON-null form. Use `?` to make it optional, or switch to a body verb (POST/PUT/PATCH).",
			reqName, f.Name, verb, svcName)
		return
	}
	if typeUnbindable != nil && typeUnbindable(f) {
		emit(f.Pos, f.Pos, CodeBindingType,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but %s can't ride a query string — switch to a body verb (POST/PUT/PATCH) so it rides @body, give it an explicit binding, or change the type",
			reqName, f.Name, verb, svcName, describeTypeRef(f.Type))
	}
}

// checkProjectBodyBindingVerb is the cross-package twin of
// checkBodyBindingVerb: the per-package pass bails for a QUALIFIED request
// type, so a `@body`/`@form` field or an auto-@query `@nullable` field
// (non-compiling) on a cross-package request on a body-less verb slipped
// through. Only qualified requests are processed (local ones owned by the
// per-package pass); the type-bindability arm is deferred (localPkg=nil).
func (r *refResolver) checkProjectBodyBindingVerb() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for svcName, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				if m == nil || m.Request == nil || m.Request.Name == nil {
					continue
				}
				switch strings.ToUpper(m.Verb) {
				case "POST", "PUT", "PATCH":
					continue
				}
				parts := m.Request.Name.Parts
				if len(parts) != 2 {
					continue
				}
				home := r.proj.Packages[parts[0]]
				if home == nil {
					continue
				}
				td, ok := home.Types[parts[1]]
				if !ok {
					continue
				}
				verb := strings.ToUpper(m.Verb)
				reqName := m.Request.Name.String()
				pathSegs := MethodRoutePathVars(m, pkg.Services)
				fields := map[string]*ast.Field{}
				r.collectGroupFieldsProject(parts[0], td.Body, fields, map[string]bool{})
				emit := func(start, end lexer.Position, code, format string, args ...any) {
					r.diag(start, lexer.SeverityError, code, format, args...)
				}
				// The IR resolves a cross-package field's element type, so a
				// foreign struct / map / nested array auto-binding to @query is
				// caught here with a position — the gap the per-package pass
				// defers to a position-less codegen error.
				unbindable := func(f *ast.Field) bool {
					return !wireBindableIR(f, r.proj)
				}
				for _, f := range fields {
					bodyBindingVerbRules(reqName, verb, svcName, pathSegs, f, unbindable, emit)
				}
			}
		}
	}
}
