package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

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
// `@status` is 1xx, 204, 205 or 304, which carry no body (RFC 9110).
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
				"@status(%d) is a no-content status and cannot carry a response body, but method %s declares one - drop the response, or use a status that allows a body.",
				code, m.Name)
			return
		}
	}
}

// checkRawModeRedundancy warns about a raw flag beside `@passthrough`, and
// `@rawRequest` with `@rawResponse`; the later decorator is reported.
func (a *analyzer) checkRawModeRedundancy(svcName string, m *ast.Method) {
	if m == nil {
		return
	}
	var passthrough, rawReq, rawResp *ast.Decorator
	for _, d := range m.Decorators {
		if d == nil {
			continue
		}
		switch d.Name {
		case wire.DecoratorPassthrough:
			for _, flag := range []*ast.Decorator{rawReq, rawResp} {
				if flag == nil {
					continue
				}
				diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeDecoratorRedundant,
					"@passthrough on method %s.%s already covers @%s - drop the flag",
					svcName, m.Name, flag.Name)
				diag.Related = related(flag.Pos, "@"+flag.Name+" declared here")
			}
			if passthrough == nil {
				passthrough = d
			}
		case wire.DecoratorRawRequest, wire.DecoratorRawResponse:
			side, other := "request", rawResp
			if d.Name == wire.DecoratorRawResponse {
				side, other = "response", rawReq
			}
			switch {
			case passthrough != nil:
				diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeDecoratorRedundant,
					"@%s is redundant on method %s.%s: @passthrough already makes the %s side raw",
					d.Name, svcName, m.Name, side)
				diag.Related = related(passthrough.Pos, "@passthrough declared here")
			case other != nil:
				diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeDecoratorRedundant,
					"@rawRequest together with @rawResponse is exactly @passthrough on method %s.%s - write @passthrough instead",
					svcName, m.Name)
				diag.Related = related(other.Pos, "@"+other.Name+" declared here")
			}
			if d.Name == wire.DecoratorRawRequest {
				if rawReq == nil {
					rawReq = d
				}
			} else if rawResp == nil {
				rawResp = d
			}
		}
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
	view, fields, ok := a.requestFields(m)
	if !ok {
		return
	}
	verb := strings.ToUpper(m.Verb)
	reqName := m.Request.Name.String()
	pathSegs := methodRoutePathVars(m, a.pkg.Services)
	for _, ff := range fields {
		a.bodyBindingVerbRules(reqName, verb, svcName, view, pathSegs, ff.Field)
	}
}

// bodyBindingVerbRules rejects `@body` and `@form` on a body-less method's
// field, and `@nullable` or an unbindable type when it auto-binds to @query;
// the field's type resolves in package view.
func (a *analyzer) bodyBindingVerbRules(reqName, verb, svcName, view string, pathSegs map[string]bool, f *ast.Field) {
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
	if !a.wireBindableIn(view, f.Type) {
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but %s can't ride a query string - switch to a body verb (POST/PUT/PATCH) so it rides @body, give it an explicit binding, or change the type",
			reqName, f.Name, verb, svcName, f.Type.String())
	}
}
