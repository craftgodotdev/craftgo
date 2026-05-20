// Cross-decorator combination rules (defaults, bindings, single-binding, passthrough body) + ref walking helpers.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkQualifiedRefs() {
	for _, td := range a.pkg.Types {
		a.walkTypeMembers(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.walkTypeMembers(ed.Name, ed.Body)
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			if m.Request != nil {
				a.checkNamedRef("method "+m.Name+" request", m.Request)
			}
			if m.Response != nil && m.Response.Type != nil {
				a.checkNamedRef("method "+m.Name+" response", m.Response.Type)
			}
		}
	}
}

// walkTypeMembers checks every Field type reference in a type or error body
// for a qualified prefix. Mixin members are skipped (see [checkQualifiedRefs]).
func (a *analyzer) walkTypeMembers(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.walkTypeRef("field "+parent+"."+f.Name, f.Type)
	}
}

// walkTypeRef descends into a TypeRef and applies the qualified-name check
// to every NamedTypeRef encountered. Map keys, map values, and generic
// arguments are all visited recursively.
func (a *analyzer) walkTypeRef(scope string, t *ast.TypeRef) {
	if t == nil {
		return
	}
	if t.Map != nil {
		a.walkTypeRef(scope, t.Map.Key)
		a.walkTypeRef(scope, t.Map.Value)
		return
	}
	if t.Named != nil {
		a.checkNamedRef(scope, t.Named)
	}
}

// checkNamedRef reports a diagnostic when n.Name has more than one segment
// and recurses through n.Args so generic arguments are validated too.
func (a *analyzer) checkNamedRef(scope string, n *ast.NamedTypeRef) {
	if n == nil || n.Name == nil {
		return
	}
	if len(n.Name.Parts) > 1 {
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"cross-package qualified reference %q in %s is not supported in v1 (folder-merge model); use the unqualified name",
			n.Name.String(), scope)
	}
	for _, arg := range n.Args {
		a.walkTypeRef(scope, arg)
	}
}

// checkCombinationRules enforces the decorator-combination contract
// documented in the README §"Combination rules":
//
//   - At most one of `@path / @query / @header / @cookie / @body / @form`
//     may appear on a single field; multiple non-body bindings would
//     compete for the same value at runtime.
//   - `@passthrough` methods may not declare `request` or `response` -
//     logic handles the wire format directly, so any framework-managed
//     shape would be silently ignored.
//   - `@default` on a non-optional field surfaces a warning - the
//     formatter auto-adds `?` on save so the OpenAPI required[] no
//     longer contradicts the default's "fires when absent" intent.
//
// Diagnostics fire on the second / conflicting decorator so the error
// points at the offending source location, not the (innocent) first
// occurrence.
func (a *analyzer) checkCombinationRules(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclCombinations(d)
		}
	}
}

// checkDeclCombinations dispatches per-declaration: type / error bodies
// for field-level rules, services / methods for method-level rules.
func (a *analyzer) checkDeclCombinations(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
	case *ast.ErrorDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			a.checkMethodCombinations(dd.Name, m)
		}
	}
}

// checkFieldCombinations applies the per-field combination checks to
// every Field in a type or error body. Mixin members are skipped - they
// have no decorators of their own.
func (a *analyzer) checkFieldCombinations(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkSingleBinding(parent, f)
		a.checkBindingFieldType(parent, f)
		a.checkDefaultNeedsOptional(parent, f)
	}
}

// checkDefaultNeedsOptional warns when a field carries `@default(...)` but
// its type does not have the `?` (optional) suffix. `@default` only fires
// when the value is absent from the request payload; in the new
// "required by default" model a non-optional field is always considered
// mandatory by OpenAPI consumers, which contradicts the default's intent.
//
// The diagnostic is a Warning, not an Error - the runtime contract still
// works (the default pre-fills the struct so validation passes when the
// client omits the field) but the OpenAPI schema published to consumers
// becomes misleading. The fix is to add `?` to the type; the formatter
// applies that fix automatically when re-emitting the source so saving
// the file resolves the warning in one round trip.
func (a *analyzer) checkDefaultNeedsOptional(parent string, f *ast.Field) {
	if f == nil || f.Type == nil || f.Type.Optional {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != "default" {
			continue
		}
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning,
			CodeDefaultNeedsOptional,
			"field %s.%s: @default implies optional - add `?` to the type (auto-fixed by `craftgo fmt` / format-on-save)",
			parent, f.Name)
		return
	}
}

// checkBindingFieldType rejects `@path`, `@header`, and `@cookie` on a
// field whose underlying primitive cannot ride the wire as a string.
// Accepts:
//
//   - the bare `string` primitive
//   - a [scalar Name string @...] declaration (its decorators inherit
//     into the field's validator chain)
//   - a string-backed enum (kind [ast.EnumBare] or [ast.EnumString])
//
// Anything else (numeric scalars / int enums / structs / arrays / maps)
// raises [CodeBindingType] - those flows would silently zero the field
// at codegen time, leaving the author with a runtime gap that no
// diagnostic explains.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field) {
	if f.Type == nil {
		return
	}
	stringOnly := map[string]bool{"path": true, "header": true, "cookie": true}
	for _, d := range f.Decorators {
		if !stringOnly[d.Name] {
			continue
		}
		if isStringBindingType(f.Type, a.pkg) {
			continue
		}
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
			"field %s.%s: @%s requires a string-backed field (string, string scalar, or string enum) - got %s",
			parent, f.Name, d.Name, describeTypeRef(f.Type))
		return
	}
}

// isStringBindingType reports whether t can ride a path / header /
// cookie wire (always a string at the protocol level). Matches:
//   - the bare `string` primitive
//   - a `scalar X string @...` declared in pkg
//   - a bare or string-valued enum declared in pkg
//
// Optional / array shapes are rejected: no clean codegen idiom
// exists for them on these wire formats yet.
func isStringBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Array || t.Optional || t.Named == nil {
		return false
	}
	name := t.Named.Name.String()
	if name == "string" {
		return true
	}
	if pkg == nil {
		return false
	}
	if sc, ok := pkg.Scalars[name]; ok && sc != nil && sc.Primitive == "string" {
		return true
	}
	if ed, ok := pkg.Enums[name]; ok && ed != nil {
		for _, m := range ed.Members {
			if v, ok := m.(*ast.EnumValue); ok {
				return v.Kind == ast.EnumBare || v.Kind == ast.EnumString
			}
		}
	}
	return false
}

// describeTypeRef renders a short human label for a TypeRef so binding
// diagnostics can say `got string?` / `got string[]` / `got int`. Kept
// minimal - the diagnostic only needs to point at the mismatch.
func describeTypeRef(t *ast.TypeRef) string {
	if t == nil {
		return "(none)"
	}
	name := "?"
	if t.Named != nil {
		name = t.Named.Name.String()
	}
	if t.Array {
		name += "[]"
	}
	if t.Optional {
		name += "?"
	}
	return name
}

// checkSingleBinding enforces the "at most one binding" rule. The
// six binding decorators (`@path / @query / @header / @cookie / @body /
// @form`) are mutually exclusive; the first wins, every subsequent one
// gets a diagnostic with a back-reference to the first.
func (a *analyzer) checkSingleBinding(parent string, f *ast.Field) {
	bindings := map[string]bool{
		"path": true, "query": true, "header": true,
		"cookie": true, "body": true, "form": true,
	}
	first := ""
	var firstPos lexer.Position
	for _, d := range f.Decorators {
		if !bindings[d.Name] {
			continue
		}
		if first == "" {
			first = d.Name
			firstPos = d.Pos
			continue
		}
		diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingConflict,
			"field %s.%s: @%s conflicts with @%s (a field must have at most one binding)",
			parent, f.Name, d.Name, first)
		diag.Related = related(firstPos, "first binding here")
	}
}

// checkMethodCombinations enforces method-level rules:
//
//   - `@passthrough` methods must not declare a `request` or `response`
//     block - logic handles the wire format directly, so any framework-
//     managed shape would be silently ignored.
func (a *analyzer) checkMethodCombinations(svcName string, m *ast.Method) {
	a.checkPassthroughBody(svcName, m)
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
