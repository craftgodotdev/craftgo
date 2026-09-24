// Package wire decides where a field's value rides (path, query, header,
// cookie, form, body, or nowhere for `@sensitive`), its wire and JSON names,
// the request auto-binding rule and a method's success status.
package wire

import (
	"net/http"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// Binding kinds: where a field's value rides. A binding decorator is named
// after its kind.
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
	// DecoratorJSON sets a body field's JSON key.
	DecoratorJSON = "json"
	// DecoratorNullable keeps a field always emitted, with null allowed.
	DecoratorNullable = "nullable"
)

// CanonicalWireName returns the collision key of a wire name: header names are
// case-insensitive (RFC 7230) and fold to lower case; other kinds pass through.
func CanonicalWireName(kind, name string) string {
	if kind == BindingHeader {
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

// WireName returns the wire name of field f under binding kind: the `@kind`
// decorator's non-empty string argument, else the field's own name.
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

// BindingKind returns the kind of the first binding decorator in ds, or ""
// when there is none. Valid input has at most one per field.
func BindingKind(ds []*ast.Decorator) string {
	for _, d := range ds {
		if d != nil && IsBindingName(d.Name) {
			return d.Name
		}
	}
	return ""
}

// IsBindingName reports whether name is a binding decorator: @path, @query,
// @header, @cookie, @body or @form.
func IsBindingName(name string) bool {
	switch name {
	case BindingPath, BindingQuery, BindingHeader, BindingCookie, BindingBody, BindingForm:
		return true
	}
	return false
}

// RequestFieldBinding returns where a request field rides: "sensitive", else
// its binding decorator's kind, else auto-bound (auto true) "path" for a
// `{param}` name or "query" on a body-less verb, else "body".
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

// JSONName returns a body field's JSON key: the non-empty `@json` argument,
// else the field's own name.
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

// NonBodyBindingKind returns a field's explicit path, query, header or cookie
// binding, or "" for a body, form, sensitive or undecorated field.
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

// StatusOverride returns the method's `@status(N)` code, if any; the analyser
// keeps it within 100..599.
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

// SuccessStatus returns the status of a method's successful response:
// `@status(N)` when set, else 204 with no response body, 201 for a POST with
// one, and 200 otherwise.
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

// Binding is where a field's value rides, as an enum of the binding kinds.
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

// String returns the binding kind; for path, query, header and cookie it is
// also the OpenAPI `in` value.
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

// BindingFromKind returns the Binding of a binding kind.
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

// ExplicitBinding returns the placement a field's own decorators declare,
// ignoring request auto-binding.
func ExplicitBinding(f *ast.Field) Binding {
	if HasSensitive(f.Decorators) {
		return BindSensitive
	}
	return BindingFromKind(BindingKind(f.Decorators))
}

// HasSensitive reports whether ds holds `@sensitive`.
func HasSensitive(ds []*ast.Decorator) bool { return ast.HasDecorator(ds, DecoratorSensitive) }
