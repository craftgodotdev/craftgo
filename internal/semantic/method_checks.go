package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func (a *analyzer) checkServiceMethods() {
	for _, si := range a.pkg.Services {
		seenName := map[string]lexer.Position{}
		seenRoute := map[string]lexer.Position{}
		for _, m := range si.Methods {
			if prev, ok := seenName[m.Name]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateMethod,
					"duplicate method %q", m.Name)
				d.Related = related(prev, "first declared here")
			} else {
				seenName[m.Name] = m.Pos
			}
			// Methods collide on verb plus resolved route shape, parameter
			// names erased; a pathless method routes by its kebab-cased name.
			rt := si.registeredRoute(m)
			key := m.Verb + " " + route.Shape(rt)
			if prev, ok := seenRoute[key]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateRoute,
					"duplicate route %q", m.Verb+" "+rt)
				d.Related = related(prev, "first declared here")
			} else {
				seenRoute[key] = m.Pos
			}
		}
	}
}

// checkRequestBodyType rejects a request type that is a builtin primitive, a
// scalar or an enum, none of which has fields to bind.
func (a *analyzer) checkRequestBodyType(m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil {
		return
	}
	pkg, sym := a.proj.resolve(a.pkg.Name, m.Request.Name)
	kind := bareRequestKind(pkg, m.Request, sym)
	if kind == "" {
		return
	}
	name := m.Request.Name.String()
	a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodeBindingType,
		"request type %q is a %s, which has no fields to bind or decode as a request body - wrap it in a type (`type Req { value %s }`)",
		name, kind, name)
}

// checkResponseBodyType rejects a builtin primitive as a response type, raw
// or not: no Go type or schema is generated for it.
func (a *analyzer) checkResponseBodyType(m *ast.Method) {
	if m == nil || m.Response == nil || m.Response.Type == nil {
		return
	}
	name := builtinClauseName(m.Response.Type)
	if name == "" {
		return
	}
	a.diag(m.Response.Pos, m.Response.Pos, lexer.SeverityError, CodeBindingType,
		"response type %q is a built-in primitive, which names no generated type to encode as a response body - wrap it in a type (`type Resp { value %s }`)",
		name, name)
}

// bareRequestKind names the fieldless kind a request clause refers to, or
// returns "" for a message; sym is n resolved in pkg.
func bareRequestKind(pkg *Package, n *ast.NamedTypeRef, sym string) string {
	if builtinClauseName(n) != "" {
		return "built-in primitive"
	}
	if pkg == nil {
		return ""
	}
	if _, ok := pkg.Scalars[sym]; ok {
		return "scalar"
	}
	if _, ok := pkg.Enums[sym]; ok {
		return "enum"
	}
	return ""
}

// builtinClauseName returns the builtin primitive a request or response
// clause names, or ""; [CodeDeclBuiltinName] keeps decls off builtin names.
func builtinClauseName(n *ast.NamedTypeRef) string {
	if n == nil || n.Name == nil || len(n.Name.Parts) != 1 {
		return ""
	}
	if name := n.Name.Parts[0]; prims.Is(name) {
		return name
	}
	return ""
}

// checkNoContentStatusBody rejects a response body on a method whose
// `@status` is 1xx, 204, 205 or 304, which carry no body (RFC 9110); decs
// are the decorators that apply to m.
func (a *analyzer) checkNoContentStatusBody(m *ast.Method, decs []*ast.Decorator) {
	if m.Response == nil || m.Response.Type == nil {
		return
	}
	code, ok := wire.StatusOverride(decs)
	if !ok || !noContentStatus(code) {
		return
	}
	d := ast.FindDecorator(decs, "status")
	a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
		"@status(%d) is a no-content status and cannot carry a response body, but method %s declares one - drop the response, or use a status that allows a body.",
		code, m.Name)
}

// noContentStatus reports whether HTTP status code carries no body.
func noContentStatus(code int) bool {
	return code == 204 || code == 205 || code == 304 || (code >= 100 && code < 200)
}

// checkRawModeRedundancy warns about a raw flag beside `@passthrough`, and
// `@rawRequest` with `@rawResponse` written before any `@passthrough`, among
// decs, the decorators that apply to m; the later decorator of each pair is
// reported.
func (a *analyzer) checkRawModeRedundancy(svcName string, m *ast.Method, decs []*ast.Decorator) {
	pass := ast.FindDecorator(decs, wire.DecoratorPassthrough)
	req := ast.FindDecorator(decs, wire.DecoratorRawRequest)
	resp := ast.FindDecorator(decs, wire.DecoratorRawResponse)
	before := func(x, y *ast.Decorator) bool { return comparePos(x.Pos, y.Pos) < 0 }
	for _, flag := range []struct {
		d    *ast.Decorator
		side string
	}{{req, "request"}, {resp, "response"}} {
		switch {
		case pass == nil || flag.d == nil:
		case before(flag.d, pass):
			diag := a.diag(pass.Pos, decoratorEnd(pass), lexer.SeverityWarning, CodeDecoratorRedundant,
				"@passthrough on method %s.%s already covers @%s - drop the flag",
				svcName, m.Name, flag.d.Name)
			diag.Related = related(flag.d.Pos, "@"+flag.d.Name+" declared here")
		default:
			diag := a.diag(flag.d.Pos, decoratorEnd(flag.d), lexer.SeverityWarning, CodeDecoratorRedundant,
				"@%s is redundant on method %s.%s: @passthrough already makes the %s side raw",
				flag.d.Name, svcName, m.Name, flag.side)
			diag.Related = related(pass.Pos, "@passthrough declared here")
		}
	}
	if req == nil || resp == nil {
		return
	}
	first, second := req, resp
	if before(resp, req) {
		first, second = resp, req
	}
	if pass == nil || before(second, pass) {
		diag := a.diag(second.Pos, decoratorEnd(second), lexer.SeverityWarning, CodeDecoratorRedundant,
			"@rawRequest together with @rawResponse is exactly @passthrough on method %s.%s - write @passthrough instead",
			svcName, m.Name)
		diag.Related = related(first.Pos, "@"+first.Name+" declared here")
	}
}

// checkBodyBindingVerb checks every request field, mixins included, of a
// method whose verb has no body.
func (a *analyzer) checkBodyBindingVerb(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil {
		return
	}
	if wire.IsBodyVerb(m.Verb) {
		return // body-bearing verbs decode @body / @form normally
	}
	view, fields, ok := a.instanceFields(m.Request)
	if !ok {
		return
	}
	verb := strings.ToUpper(m.Verb)
	reqName := m.Request.Name.String()
	pathSegs := methodRoutePathVars(m, a.pkg.Services)
	for _, ff := range fields {
		a.bodyBindingVerbRules(reqName, verb, svcName, view, pathSegs, ff)
	}
}

// checkMultipartParts rejects a part of m's multipart request - a body or
// form field, mixins included, beside a `file` - that the form binder cannot
// fill: an optional type parameter over a `file` or an array, whose Go value
// is a pointer to the file or the slice, or a text part of a type no form
// value carries; decs are the decorators that apply to m. A raw request is not
// bound, and an explicit @form or a field holding a `file` below the top level
// is reported where it is declared.
func (a *analyzer) checkMultipartParts(svcName string, m *ast.Method, decs []*ast.Decorator) {
	if m == nil || m.Request == nil || !wire.IsBodyVerb(m.Verb) {
		return
	}
	if rawReq, _ := wire.RawSides(decs); rawReq {
		return
	}
	view, fields, ok := a.instanceFields(m.Request)
	if !ok {
		return
	}
	pathSegs := methodRoutePathVars(m, a.pkg.Services)
	var parts []FlatField
	multipart := false
	for _, ff := range fields {
		if b, _ := wire.RequestFieldBinding(ff.Field, pathSegs, true); b == wire.BindBody || b == wire.BindForm {
			parts = append(parts, ff)
			multipart = multipart || isFileTypeRef(ff.Field.Type)
		}
	}
	if !multipart {
		return
	}
	verb, reqName := strings.ToUpper(m.Verb), m.Request.Name.String()
	for _, ff := range parts {
		f := ff.Field
		switch {
		case ast.HasDecorator(f.Decorators, wire.BindingForm):
		case ff.optionalParam && isFileTypeRef(f.Type):
			arg := *f.Type
			arg.Optional = false
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
				"field %s.%s: on the %s %s handler this rides a multipart file part, but it is an optional type parameter over a file (%s), whose Go value is a pointer the multipart binder cannot fill - drop the `?` from the type parameter (a file is already nilable)",
				reqName, f.Name, verb, svcName, arg.String())
		case holdsFile(f.Type):
		case ff.sliceBehindPointer():
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
				"field %s.%s: on the %s %s handler this rides a multipart form part (the request carries a file), but it is an optional type parameter over an array, whose Go value is a pointer to a slice the form binder cannot fill - drop the `?` from the type parameter (an array is already nilable)",
				reqName, f.Name, verb, svcName)
		case !a.proj.wireBindable(view, f.Type):
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
				"field %s.%s: on the %s %s handler this rides a multipart form part (the request carries a file), but %s is no form value - a part carries string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or a single-level array of those (no maps, structs, generic instantiations or nested arrays); split it into such fields, or send it in a request without a file",
				reqName, f.Name, verb, svcName, f.Type.String())
		}
	}
}

// bodyBindingVerbRules rejects `@body` and `@form` on a body-less method's
// field, and `@nullable` or an unbindable type when it auto-binds to @query;
// the field's type resolves in package view.
func (a *analyzer) bodyBindingVerbRules(reqName, verb, svcName, view string, pathSegs map[string]bool, ff FlatField) {
	f := ff.Field
	if f == nil {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || (d.Name != wire.BindingBody && d.Name != wire.BindingForm) {
			continue
		}
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingVerb,
			"field %s.%s: @%s requires a body-bearing verb (POST/PUT/PATCH) - the %s %s handler decodes no request body, so the field would be silently dropped",
			reqName, f.Name, d.Name, verb, svcName)
		break // one diagnostic per field
	}
	if f.Type == nil {
		return
	}
	if b, auto := wire.RequestFieldBinding(f, pathSegs, false); b != wire.BindQuery || !auto {
		return
	}
	if ast.HasDecorator(f.Decorators, "nullable") {
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but @nullable has no meaning on a wire parameter - a query string has no JSON-null form. Use `?` to make it optional, or switch to a body verb (POST/PUT/PATCH).",
			reqName, f.Name, verb, svcName)
		return
	}
	if ff.sliceBehindPointer() {
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but it is an optional type parameter over an array, whose Go value is a pointer to a slice the query binder cannot fill - drop the `?` from the type parameter (an array is already nilable), or switch to a body verb (POST/PUT/PATCH)",
			reqName, f.Name, verb, svcName)
		return
	}
	if !a.proj.wireBindable(view, f.Type) {
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but %s can't ride a query string - switch to a body verb (POST/PUT/PATCH) so it rides @body, give it an explicit binding, or change the type",
			reqName, f.Name, verb, svcName, f.Type.String())
	}
}
