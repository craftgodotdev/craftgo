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
		if d == nil {
			continue
		}
		switch d.Name {
		case BindingPath, BindingQuery, BindingHeader, BindingCookie, BindingBody, BindingForm:
			return d.Name
		}
	}
	return ""
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
