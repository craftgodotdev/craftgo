// Package wire decides where a field's value rides (path, query, header,
// cookie, form, body, or nowhere for `@sensitive`), its wire and JSON names,
// the request auto-binding rule and a method's success status.
package wire

import (
	"net/http"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// Binding decorator names; each declares the [Binding] its name spells.
const (
	BindingPath   = "path"
	BindingQuery  = "query"
	BindingHeader = "header"
	BindingCookie = "cookie"
	BindingForm   = "form"
	BindingBody   = "body"
)

// DecoratorJSON sets a body field's JSON key.
const DecoratorJSON = "json"

// Binding is where a field's value rides.
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

// String returns the name of the decorator that declares b; for path, query,
// header and cookie it is also the OpenAPI `in` value.
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
		return "sensitive"
	default:
		return BindingBody
	}
}

// IsParam reports whether b places a field in a named parameter - a path
// segment, a query, a header, a cookie or a form part - rather than the body.
func (b Binding) IsParam() bool {
	switch b {
	case BindPath, BindQuery, BindHeader, BindCookie, BindForm:
		return true
	}
	return false
}

// bindingNamed returns the binding a binding decorator called name declares.
func bindingNamed(name string) (Binding, bool) {
	for _, b := range [...]Binding{BindPath, BindQuery, BindHeader, BindCookie, BindBody, BindForm} {
		if b.String() == name {
			return b, true
		}
	}
	return BindBody, false
}

// CanonicalWireName returns the collision key of a wire name: header names are
// case-insensitive (RFC 7230) and fold to lower case; other bindings pass through.
func CanonicalWireName(b Binding, name string) string {
	if b == BindHeader {
		return strings.ToLower(name)
	}
	return name
}

// IsBodyVerb reports whether verb carries a request body: POST, PUT or PATCH.
func IsBodyVerb(verb string) bool {
	switch strings.ToUpper(verb) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// WireName returns the wire name of field f under binding b: the binding
// decorator's non-empty string argument, else the field's own name.
func WireName(f *ast.Field, b Binding) string { return nameArg(f, b.String()) }

// nameArg returns the non-empty string argument of f's `@decorator`, else
// f's own name.
func nameArg(f *ast.Field, decorator string) string {
	if f == nil {
		return ""
	}
	if s, ok := ast.StringArg(f.Decorators, decorator); ok && s != "" {
		return s
	}
	return f.Name
}

// BindingKind returns the binding of the first binding decorator in ds, and
// false with [BindBody] when there is none. Valid input has at most one per
// field.
func BindingKind(ds []*ast.Decorator) (Binding, bool) {
	for _, d := range ds {
		if d == nil {
			continue
		}
		if b, ok := bindingNamed(d.Name); ok {
			return b, true
		}
	}
	return BindBody, false
}

// IsBindingName reports whether name is a binding decorator: @path, @query,
// @header, @cookie, @body or @form.
func IsBindingName(name string) bool {
	_, ok := bindingNamed(name)
	return ok
}

// RequestFieldBinding returns where a request field rides: [BindSensitive],
// else its binding decorator's binding, else auto-bound (auto true)
// [BindPath] for a `{param}` name or [BindQuery] on a body-less verb, else
// [BindBody].
func RequestFieldBinding(f *ast.Field, pathNames map[string]bool, bodyVerb bool) (b Binding, auto bool) {
	if HasSensitive(f.Decorators) {
		return BindSensitive, false
	}
	if b, ok := BindingKind(f.Decorators); ok {
		return b, false
	}
	switch {
	case pathNames[f.Name]:
		return BindPath, true
	case !bodyVerb:
		return BindQuery, true
	default:
		return BindBody, false
	}
}

// Raw-mode decorator names: `@passthrough` hands both transport sides to
// logic, each flag one side.
const (
	DecoratorPassthrough = "passthrough"
	DecoratorRawRequest  = "rawRequest"
	DecoratorRawResponse = "rawResponse"
)

// RawSides reports which transport sides logic owns for a method with
// decorators ds; the transport never binds or encodes a raw side.
func RawSides(ds []*ast.Decorator) (rawRequest, rawResponse bool) {
	if ast.HasDecorator(ds, DecoratorPassthrough) {
		return true, true
	}
	return ast.HasDecorator(ds, DecoratorRawRequest), ast.HasDecorator(ds, DecoratorRawResponse)
}

// JSONPresence is how a field appears in the JSON body.
type JSONPresence int

const (
	// JSONAbsent - bound outside the body, or `@sensitive`.
	JSONAbsent JSONPresence = iota
	// JSONRequired - always emitted, never null.
	JSONRequired
	// JSONOptional - may be absent (`T?`).
	JSONOptional
	// JSONNullable - always emitted, may be null (`T @nullable`).
	JSONNullable
)

// JSONShape returns a field's JSON key and how it appears in the JSON body.
// `?` wins over `@nullable`, so a missing value is omitted, not sent as null;
// an `@form` field counts as a body field.
func JSONShape(f *ast.Field) (name string, presence JSONPresence) {
	if f == nil {
		return "", JSONAbsent
	}
	name = JSONName(f)
	if _, offBody := NonBodyBindingKind(f); offBody || HasSensitive(f.Decorators) {
		return name, JSONAbsent
	}
	switch {
	case f.Type != nil && f.Type.Optional:
		return name, JSONOptional
	case ast.HasDecorator(f.Decorators, "nullable"):
		return name, JSONNullable
	}
	return name, JSONRequired
}

// JSONName returns a body field's JSON key: the non-empty `@json` argument,
// else the field's own name.
func JSONName(f *ast.Field) string { return nameArg(f, DecoratorJSON) }

// NonBodyBindingKind returns a field's explicit path, query, header or cookie
// binding, and false for a body, form, sensitive or undecorated field.
func NonBodyBindingKind(f *ast.Field) (Binding, bool) {
	if f == nil {
		return BindBody, false
	}
	switch b, _ := BindingKind(f.Decorators); b {
	case BindPath, BindQuery, BindHeader, BindCookie:
		return b, true
	}
	return BindBody, false
}

// StatusOverride returns the `@status(N)` code of a method with decorators
// ds, if any; the analyser keeps it within 100..599.
func StatusOverride(ds []*ast.Decorator) (int, bool) {
	if code, ok := ast.Arg[*ast.IntLit](ds, "status"); ok {
		return int(code.Value), true
	}
	return 0, false
}

// SuccessStatus returns the status of m's successful response, ds being the
// decorators that apply to it: `@status(N)` when set, else 204 with no
// response body, 201 for a POST with one, and 200 otherwise.
func SuccessStatus(m *ast.Method, ds []*ast.Decorator) int {
	if code, ok := StatusOverride(ds); ok {
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

// ExplicitBinding returns the placement a field's own decorators declare,
// ignoring request auto-binding.
func ExplicitBinding(f *ast.Field) Binding {
	if HasSensitive(f.Decorators) {
		return BindSensitive
	}
	b, _ := BindingKind(f.Decorators)
	return b
}

// HasSensitive reports whether ds holds `@sensitive`.
func HasSensitive(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, "sensitive") }
