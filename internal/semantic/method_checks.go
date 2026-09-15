// Method-level combination checks: request body type, body-verb rules,
// @status(204) bodies, and raw-mode redundancy.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// methodLabel renders the diagnostic phrase for one method, e.g.
// "method Users.Create".
func methodLabel(svc string, m *ast.Method) string {
	return "method " + svc + "." + m.Name
}

// checkRequestBodyType rejects a request type that is a bare scalar or enum
// (a fieldless named type). The request binder/decoder drives off the
// type's FIELDS, so a fieldless type yields no decode and no parameters -
// the client payload is silently dropped (and a constraint-free scalar
// produces non-compiling Go, since the handler calls a Validate() that
// isn't generated). Wrap the value in a `type { value <T> }`. Mirrors the
// existing bare-array request reject.
func (a *analyzer) checkRequestBodyType(m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil {
		return
	}
	pkg, sym := a.resolveNamed(a.pkg.Name, m.Request)
	kind := bareRequestKind(pkg, sym)
	if kind == "" {
		return
	}
	name := m.Request.Name.String()
	a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodeBindingType,
		"request type %q is a %s, which has no fields to bind or decode as a request body - wrap it in a type (`type Req { value %s }`)",
		name, kind, name)
}

// bareRequestKind reports whether `name` resolves to a scalar or enum in pkg
// (a fieldless type that has nothing to bind or decode as a request body), or
// "" otherwise.
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

// checkNoContentStatusBody rejects a no-content success status (204, 304,
// or any 1xx) on a method that declares a response body. Per RFC 9110
// those statuses carry no body, but both the OpenAPI emitter and the
// transport template select their body-emitting branch on response-body
// presence alone - never the status - so the pairing would advertise a
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
				"@status(%d) is a no-content status and cannot carry a response body, but method %s declares one - drop the response, or use a status that allows a body.",
				code, m.Name)
			return
		}
	}
}

// checkRawModeRedundancy warns when a method spells a raw side twice.
// `@passthrough` already hands both sides to logic, so `@rawRequest` /
// `@rawResponse` next to it add nothing; and `@rawRequest @rawResponse`
// together is exactly `@passthrough`. Codegen reads the modes through
// wire.RawSides, so the output is identical either way - the diagnostic
// is a warning, anchored on the later decorator with the earlier one as
// related context (the same "second occurrence is the offender" rule
// the other combination checks follow). Decorators propagated from an
// `extend service` header sit before the method's own, so a method-level
// flag that repeats a header-level `@passthrough` is anchored on the
// method's line.
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

// checkBodyBindingVerb rejects `@body` / `@form` request fields on a
// non-body verb (GET / HEAD / DELETE / OPTIONS). Those handlers never
// decode a request body, so the binder's switch falls through and the
// field is left zero with no error - silent data loss. The OpenAPI side
// likewise omits the requestBody for non-body verbs, so the contract and
// the runtime agree only by both dropping the field. Reject up front.
//
// The request type is flattened so a field a request inherits through a
// mixin is checked too - mirroring the codegen request flatten. Body verbs
// route `@body` through the JSON decoder and `@form` through the multipart
// handler, so the check only fires for the non-body set.
func (a *analyzer) checkBodyBindingVerb(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil {
		return
	}
	if wire.IsBodyVerb(m.Verb) {
		return // body-bearing verbs decode @body / @form normally
	}
	td, fields := a.requestFields(m)
	if td == nil {
		return
	}
	verb := strings.ToUpper(m.Verb)
	reqName := m.Request.Name.String()
	pathSegs := MethodRoutePathVars(m, a.pkg.Services)
	for _, pf := range fields {
		a.bodyBindingVerbRules(reqName, verb, svcName, pathSegs, pf)
	}
}

// bodyBindingVerbRules checks one request field of a NON-body-verb method:
// `@body` / `@form` require a body-bearing verb (the handler decodes no body,
// so the field would be silently dropped); an un-decorated field auto-binds
// to @query, where `@nullable` is meaningless (a query string has no
// JSON-null form, and the pointer it lowers to can't take the binder's plain
// string - non-compiling); and a type that cannot ride a query string is
// rejected. The field's type resolves in the package that declares it.
func (a *analyzer) bodyBindingVerbRules(reqName, verb, svcName string, pathSegs map[string]bool, pf promotedField) {
	f := pf.Field
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
	if kind, auto := wire.RequestFieldBinding(f, pathSegs, false); kind != wire.BindingQuery || !auto {
		return
	}
	if ast.HasDecorator(f.Decorators, "nullable") {
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but @nullable has no meaning on a wire parameter - a query string has no JSON-null form. Use `?` to make it optional, or switch to a body verb (POST/PUT/PATCH).",
			reqName, f.Name, verb, svcName)
		return
	}
	if !a.wireBindableIn(pf.Pkg, f.Type) {
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but %s can't ride a query string - switch to a body verb (POST/PUT/PATCH) so it rides @body, give it an explicit binding, or change the type",
			reqName, f.Name, verb, svcName, describeTypeRef(f.Type))
	}
}
