package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
)

// checkCombinationRules runs the field and method combination rules over
// every declaration.
func (a *analyzer) checkCombinationRules(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclCombinations(d)
		}
	}
}

// checkDeclCombinations runs the field rules on a type or error body and the
// method rules on each method of a service.
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

// checkFieldCombinations checks every field of a type or error body.
func (a *analyzer) checkFieldCombinations(parent string, members []ast.TypeMember) {
	for _, f := range ast.Fields(members) {
		a.checkSingleBinding(parent, f)
		a.checkBindingFieldType(parent, f)
		a.checkBoundOverlap(parent, f)
	}
}

// checkMethodCombinations runs the method-level rules on m.
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
