// Cross-cutting decorator utilities: presence checks and string-argument
// extraction shared across the transport, types, and OpenAPI emitters.
package codegen

import (
	"net/http"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// hasDeprecatedDecorator reports whether `@deprecated` is declared in
// the chain. Used by OpenAPI codegen to flag operations and schemas,
// and by the types emitter to prepend a Go-style `// Deprecated:` line
// (which `go vet` / `staticcheck` honour).
func hasDeprecatedDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "deprecated") }

// deprecatedReason returns the optional `@deprecated("...")` argument,
// or "" when the decorator carries no message. Both forms are valid:
// `@deprecated` alone is "deprecated, no reason given"; `@deprecated("use Foo")`
// supplies the reason that ends up in `// Deprecated:` comments and
// OpenAPI descriptions.
func deprecatedReason(ds []*ast.Decorator) string {
	return decoratorStringArg(ds, "deprecated")
}

// decoratorStringArg returns the first positional string argument of
// the named decorator, or "" when absent. Used for simple
// "decorator-with-text" forms: `@doc("...")`, `@summary("...")`,
// `@deprecated("...")`. Multiple-argument or object-form decorators
// have their own dedicated extractors.
func decoratorStringArg(ds []*ast.Decorator, name string) string {
	for _, d := range ds {
		if d.Name != name {
			continue
		}
		if len(d.Args) == 0 {
			return ""
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return s.Value
		}
	}
	return ""
}

// resolveDescription returns the OpenAPI description for a node by
// preferring the explicit `@doc("...")` decorator over the leading `//`
// comment block. Both forms are documented in the README; `@doc` wins
// because it's an intentional override, while comments are often
// implementation notes the API consumer doesn't care about.
func resolveDescription(decs []*ast.Decorator, doc []string) string {
	if s := decoratorStringArg(decs, "doc"); s != "" {
		return s
	}
	if len(doc) == 0 {
		return ""
	}
	return strings.Join(doc, "\n")
}

// hasNullableDecorator reports whether `@nullable` appears on the
// field. The DSL already has `T?` for "optional" (field can be absent);
// `@nullable` is the orthogonal "value can be null when present" flag,
// surfaced via OpenAPI's null-type entry so spec consumers know
// `null` is a valid wire value.
func hasNullableDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "nullable") }

// hasSensitiveDecorator reports whether ds contains the `@sensitive`
// marker. Sensitive fields are server-only: they get `json:"-"` in
// the Go struct (so neither the JSON decoder nor the encoder touches
// them) and are skipped entirely from the OpenAPI spec.
func hasSensitiveDecorator(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "sensitive") }

// methodSuccessStatus resolves the success status code for a method
// whose response the framework writes. The transport handler and the
// OpenAPI spec both call this so they always agree on the same code.
// `@status(N)` wins; otherwise the default is verb-aware:
//
//   - no response body           → 204 No Content
//   - POST returning a body       → 201 Created
//   - any other verb with a body  → 200 OK
//
// The "no body → 204" rule deliberately takes precedence over the verb
// default: a POST that returns nothing is 204, not 201.
func methodSuccessStatus(m *ast.Method) int {
	if code, ok := statusOverride(m); ok {
		return code
	}
	if m.Response == nil || m.Response.Type == nil {
		return http.StatusNoContent
	}
	if strings.EqualFold(m.Verb, "post") {
		return http.StatusCreated
	}
	return http.StatusOK
}

// bindingFromDecorators returns the OpenAPI `in` string implied by a
// field-binding decorator, or "" when the field has no explicit binding.
// `@body` and `@form` are returned verbatim so the caller can recognise
// and skip them - body-shaped fields land in requestBody, not parameters.
func bindingFromDecorators(ds []*ast.Decorator) string {
	return wire.BindingKind(ds)
}

// fieldIsRequired is THE spec-required rule - craftgo's "required by
// default" model: a field must be present unless its type carries the `?`
// suffix, OR it carries `@default` (the transport pre-fills the default
// before decode, so an absent value is valid; advertising it required would
// contradict the very default the schema carries). ResolvedField.SpecRequired
// is computed from this function; raw-field call sites (the parameter/body
// emitters, which work from field bins) call it directly - one rule, one
// function.
func fieldIsRequired(f *ast.Field) bool {
	if f == nil || f.Type == nil || f.Type.Optional {
		return false
	}
	return !ast.HasDecorator(f.Decorators, "default")
}

// bindingWireName returns the wire-side parameter name for a bound
// field. The default is the DSL field name; an explicit string
// argument on the binding decorator (`@path("user_id")`,
// `@header("X-API-Key")`, etc.) overrides it so wire-side conventions
// (snake_case path segments, kebab/hyphen HTTP headers) can differ
// from the Go field name. `kind` selects which decorator to inspect
// (`path`/`query`/`header`/`cookie`) so the same field may carry the
// wrong-decorator's arg without leakage. The rule lives in
// [wire.WireName] so the analyser's binding checks and these binders
// agree on the emitted name.
func bindingWireName(f *ast.Field, kind string) string {
	return wire.WireName(f, kind)
}

// statusOverride returns the explicit `@status(N)` code declared on the
// method, if any. The value is range-validated (100..599) by the
// semantic layer, so codegen can trust it.
func statusOverride(m *ast.Method) (int, bool) {
	for _, d := range m.Decorators {
		if d == nil || d.Name != "status" || len(d.Args) == 0 {
			continue
		}
		if i, ok := d.Args[0].Value.(*ast.IntLit); ok {
			return int(i.Value), true
		}
	}
	return 0, false
}
