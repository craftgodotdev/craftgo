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
			"cross-package qualified reference %q in %s is not supported (folder-merge model); use the unqualified name",
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
		a.checkDuplicateWireNames(dd.Name, dd.Body)
	case *ast.ErrorDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
		a.checkDuplicateWireNames(dd.Name, dd.Body)
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			a.checkMethodCombinations(dd, m)
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
		a.checkBoundOverlap(parent, f)
	}
}

// checkMethodCombinations enforces method-level rules:
//
//   - `@passthrough` methods must not declare a `request` or `response`
//     block - logic handles the wire format directly, so any framework-
//     managed shape would be silently ignored.
func (a *analyzer) checkMethodCombinations(svc *ast.ServiceDecl, m *ast.Method) {
	svcName := svc.Name
	a.checkPassthroughBody(svcName, m)
	a.checkBodyBindingVerb(svcName, m)
	a.checkDuplicatePathVars(svc, m)
	a.checkAutoPathField(m)
	a.checkDuplicateAutoWireNames(m)
	a.checkNoContentStatusBody(m)
	a.checkRequestBodyType(m)
}
