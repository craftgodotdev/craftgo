// Package wire is the leaf vocabulary of craftgo's wire bindings: where a
// field's value rides (path / query / header / cookie / form / body /
// sensitive), how its wire name is derived, and the request auto-binding
// rule. It sits below both the analyzer and codegen - each rule here is
// decided exactly once and every layer imports the same answer, so the
// editor's diagnostics, the generated binder, and the OpenAPI document
// cannot disagree on where a field rides or what it is called.
package wire

import (
	"net/http"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// Binding-kind vocabulary: the wire-placement names shared by the analyzer
// and codegen (for the binding decorators, the decorator name IS the kind).
// Kind comparisons and cross-layer calls use these constants so a typo fails
// to compile instead of silently never matching.
const (
	BindingPath      = "path"
	BindingQuery     = "query"
	BindingHeader    = "header"
	BindingCookie    = "cookie"
	BindingForm      = "form"
	BindingBody      = "body"
	BindingSensitive = "sensitive"
)

// Field decorators that shape the JSON body without naming a binding.
const (
	// DecoratorSensitive keeps a field off the wire in both directions.
	DecoratorSensitive = "sensitive"
	// DecoratorJSON names a body field on the wire when the JSON key is
	// not the field name - a contract another system owns.
	DecoratorJSON = "json"
	// DecoratorNullable keeps a field always-emitted with a null value
	// allowed.
	DecoratorNullable = "nullable"
)

// CanonicalWireName folds a wire name to its collision key. HTTP header names
// are case-insensitive (RFC 7230) and net/http canonicalises them, so two
// fields bound to `X-Trace` and `x-trace` reach the same header - fold header
// names to lower case for the key. Path / query / cookie names are
// case-sensitive on the wire and pass through unchanged.
func CanonicalWireName(kind, name string) string {
	if kind == BindingHeader {
		return strings.ToLower(name)
	}
	return name
}

// IsBodyVerb reports whether verb carries a request body (POST/PUT/PATCH) -
// the condition under which an undecorated field rides @body rather than
// auto-promoting to @query.
func IsBodyVerb(verb string) bool {
	switch strings.ToUpper(verb) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// WireName returns the on-the-wire name for field f under binding kind
// (path/query/header/cookie/form): the binding decorator's first non-empty
// string argument, or the field's own name when none is given. It is the one
// rule shared by the analyser's binding checks and codegen's binders /
// OpenAPI parameter emit, so the documented parameter name and the name the
// handler actually reads cannot disagree.
func WireName(f *ast.Field, kind string) string {
	if f == nil {
		return ""
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != kind || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
			return s.Value
		}
	}
	return f.Name
}

// BindingKind returns the binding kind named by the first binding decorator
// in ds - "path" / "query" / "header" / "cookie" / "body" / "form" - or "" when
// none is present. It is the single "which decorator binds this field"
// classifier the analyser's binding checks and codegen's binders both read, so
// the two layers cannot disagree on where a field rides. (Valid input carries
// at most one binding decorator per field - the single-binding rule rejects
// the rest - so first-match is unambiguous.)
func BindingKind(ds []*ast.Decorator) string {
	for _, d := range ds {
		if d != nil && IsBindingName(d.Name) {
			return d.Name
		}
	}
	return ""
}

// IsBindingName reports whether name is one of the six wire-binding decorator
// names (@path / @query / @header / @cookie / @body / @form).
func IsBindingName(name string) bool {
	switch name {
	case BindingPath, BindingQuery, BindingHeader, BindingCookie, BindingBody, BindingForm:
		return true
	}
	return false
}

// RequestFieldBinding resolves where a request field rides once method
// context is applied: its explicit binding decorator if any (or @sensitive),
// otherwise the auto-binding rule - an un-decorated field auto-binds to "path"
// when its name matches a `{param}` segment, to "query" on a body-less verb
// (there is no body to decode into), or stays "body". auto is true only for an
// auto-promoted path/query field. This is the single place the request
// auto-binding rule lives, read by both the analyser's binding checks and
// codegen's request resolver so the two cannot disagree on where a field rides.
func RequestFieldBinding(f *ast.Field, pathNames map[string]bool, bodyVerb bool) (kind string, auto bool) {
	if ast.HasDecorator(f.Decorators, "sensitive") {
		return BindingSensitive, false
	}
	if k := BindingKind(f.Decorators); k != "" {
		return k, false
	}
	switch {
	case pathNames[f.Name]:
		return BindingPath, true
	case !bodyVerb:
		return BindingQuery, true
	default:
		return BindingBody, false
	}
}

// Raw-mode decorator names. `@passthrough` hands both transport sides to
// logic; the two flags hand over one side each. Every layer that needs
// to know who owns a side reads [RawSides] rather than matching these
// names itself.
const (
	DecoratorPassthrough = "passthrough"
	DecoratorRawRequest  = "rawRequest"
	DecoratorRawResponse = "rawResponse"
)

// RawSides reports which transport sides logic owns for a method with
// decorators ds. A raw request side skips bind + validate and hands
// logic the *http.Request; a raw response side skips the response
// encode and hands logic the http.ResponseWriter. `@passthrough` is
// exactly `@rawRequest @rawResponse`. A `request` / `response` block on
// a raw side is a docs-only contract: the OpenAPI document and the
// generated Go types describe it, the transport never touches it.
func RawSides(ds []*ast.Decorator) (rawRequest, rawResponse bool) {
	if ast.HasDecorator(ds, DecoratorPassthrough) {
		return true, true
	}
	return ast.HasDecorator(ds, DecoratorRawRequest), ast.HasDecorator(ds, DecoratorRawResponse)
}

// JSONPresence is how a field appears in the JSON body.
type JSONPresence int

const (
	// JSONAbsent - the field never rides the JSON body: it is bound to
	// the URL, a header or a cookie, or it is `@sensitive`.
	JSONAbsent JSONPresence = iota
	// JSONRequired - always emitted, never null.
	JSONRequired
	// JSONOptional - may be absent (`T?`).
	JSONOptional
	// JSONNullable - always emitted, may be null (`T @nullable`).
	JSONNullable
)

// JSONShape reports the wire name a field carries in the JSON body and
// whether it is present, optional, nullable, or off the body entirely.
// The Go struct tag and every language target read it, so a generated
// type in any language describes the same JSON.
//
// `?` dominates `@nullable`: the wire contract is "may be absent", so a
// missing value is omitted rather than sent as an explicit null.
// `@form` stays in the body - the multipart handler binds its own table.
func JSONShape(f *ast.Field) (name string, presence JSONPresence) {
	if f == nil {
		return "", JSONAbsent
	}
	name = JSONName(f)
	if NonBodyBindingKind(f) != "" || ast.HasDecorator(f.Decorators, DecoratorSensitive) {
		return name, JSONAbsent
	}
	switch {
	case f.Type != nil && f.Type.Optional:
		return name, JSONOptional
	case ast.HasDecorator(f.Decorators, DecoratorNullable):
		return name, JSONNullable
	}
	return name, JSONRequired
}

// JSONName is the key a body field carries in JSON: the `@json` argument
// when the field has one, else the field's own name.
func JSONName(f *ast.Field) string {
	if f == nil {
		return ""
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != DecoratorJSON || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
			return s.Value
		}
	}
	return f.Name
}

// NonBodyBindingKind returns the wire location an explicit binding
// decorator places a field in - path / query / header / cookie, the
// bindings served outside the JSON body - or "" for a body, form,
// sensitive or undecorated field.
func NonBodyBindingKind(f *ast.Field) string {
	if f == nil {
		return ""
	}
	switch kind := BindingKind(f.Decorators); kind {
	case BindingPath, BindingQuery, BindingHeader, BindingCookie:
		return kind
	}
	return ""
}

// StatusOverride returns the explicit `@status(N)` code on the method, if
// any. The value is range-validated (100..599) by the semantic layer.
func StatusOverride(m *ast.Method) (int, bool) {
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

// SuccessStatus resolves the status a method's successful response carries.
// `@status(N)` wins; otherwise the default is verb-aware:
//
//   - no response body          -> 204 No Content
//   - POST returning a body     -> 201 Created
//   - any other verb with a body -> 200 OK
//
// The "no body -> 204" rule takes precedence over the verb default: a POST
// that returns nothing is 204, not 201.
func SuccessStatus(m *ast.Method) int {
	if code, ok := StatusOverride(m); ok {
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

// Binding is where a field's value rides. It is the enum form of the
// BindingKind strings, so a stage can switch on placement without
// comparing text.
type Binding int

const (
	BindBody Binding = iota // JSON request/response body (the default)
	BindPath
	BindQuery
	BindHeader
	BindCookie
	BindForm
	BindSensitive // @sensitive: server-only, json:"-", excluded everywhere
)

// String renders the OpenAPI `in` value; body and sensitive have no `in`.
func (b Binding) String() string {
	switch b {
	case BindPath:
		return BindingPath
	case BindQuery:
		return BindingQuery
	case BindHeader:
		return BindingHeader
	case BindCookie:
		return BindingCookie
	case BindForm:
		return BindingForm
	case BindSensitive:
		return BindingSensitive
	default:
		return BindingBody
	}
}

// BindingFromKind maps a BindingKind string onto its enum form.
func BindingFromKind(kind string) Binding {
	switch kind {
	case BindingPath:
		return BindPath
	case BindingQuery:
		return BindQuery
	case BindingHeader:
		return BindHeader
	case BindingCookie:
		return BindCookie
	case BindingForm:
		return BindForm
	case BindingSensitive:
		return BindSensitive
	default:
		return BindBody
	}
}

// ExplicitBinding is the placement a field's own decorators declare,
// before any request auto-binding.
func ExplicitBinding(f *ast.Field) Binding {
	if HasSensitive(f.Decorators) {
		return BindSensitive
	}
	return BindingFromKind(BindingKind(f.Decorators))
}

// HasSensitive reports whether `@sensitive` marks the field server-only.
func HasSensitive(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, DecoratorSensitive) }
