// Cross-decorator combination rules (defaults, bindings, single-binding, raw-mode redundancy).
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// checkCombinationRules enforces the decorator-combination contract
// documented in the README §"Combination rules":
//
//   - At most one of `@path / @query / @header / @cookie / @body / @form`
//     may appear on a single field; multiple non-body bindings would
//     compete for the same value at runtime.
//   - `@passthrough` / `@rawRequest` / `@rawResponse` combine freely with
//     `request` / `response` blocks (a block on a raw side is a docs-only
//     contract); spelling a raw side twice is redundant and warns.
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
//   - `@passthrough` next to `@rawRequest` / `@rawResponse`, or both
//     flags together, is redundant - warning on the later decorator.
func (a *analyzer) checkMethodCombinations(svc *ast.ServiceDecl, m *ast.Method) {
	svcName := svc.Name
	a.checkRawModeRedundancy(svcName, m)
	a.checkBodyBindingVerb(svcName, m)
	a.checkDuplicatePathVars(svc, m)
	a.checkAutoPathField(m)
	a.checkDuplicateAutoWireNames(m)
	a.checkNoContentStatusBody(m)
	a.checkRequestBodyType(m)
	a.checkResponseBodyType(m)
}
