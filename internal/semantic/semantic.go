// Package semantic analyses parsed design files: [AnalyzeProject] groups
// them into packages, builds each package's symbol tables and reports
// every rule violation as a [Diagnostic] with a stable code. It also
// resolves the target-independent facts about fields, events and enums.
package semantic

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Diagnostic is [lexer.Diagnostic].
type Diagnostic = lexer.Diagnostic

// Package is the merged result of analysing the files of one DSL package.
// Every map is keyed by the unqualified declaration name.
type Package struct {
	// Name is the name the package's files declare.
	Name        string
	Types       map[string]*ast.TypeDecl
	Enums       map[string]*ast.EnumDecl
	Errors      map[string]*ast.ErrorDecl
	Scalars     map[string]*ast.ScalarDecl
	Middlewares map[string]*ast.MiddlewareDecl
	Services    map[string]*ServiceInfo
	// Events is its own namespace, so an event may share its payload type's name.
	Events map[string]*ast.EventDecl
}

// ServiceInfo is a primary `service` declaration and its `extend service`
// blocks. Methods lists the primary's methods, then each block's.
type ServiceInfo struct {
	Primary *ast.ServiceDecl
	Extends []*ast.ServiceDecl
	Methods []*ast.Method
}

// Options carries the manifest settings the analysis checks against.
type Options struct {
	// SecuritySchemes lists the manifest's openapi.securitySchemes names;
	// nil skips the @security reference check.
	SecuritySchemes []string

	// BasePath is the manifest's openapi.basePath, the prefix of every route.
	BasePath string

	// HealthPaths replaces the reserved health routes (/healthz, /readyz)
	// when non-empty.
	HealthPaths []string

	// DesignRoot is the design folder `import "path"` resolves against;
	// empty skips the import path check.
	DesignRoot string

	// FileCase is the manifest's output.fileCase ("kebab", "snake" or
	// "camel"), which names an ungrouped service's directory; empty means snake.
	FileCase string
}

// Analyze runs [AnalyzeProject] with zero [Options] and returns the
// project's first package by name, or an empty package when it has none.
func Analyze(files []*ast.File) (*Package, []Diagnostic) {
	return analyzeWith(files, Options{})
}

// analyzeWith is [Analyze] with opts.
func analyzeWith(files []*ast.File, opts Options) (*Package, []Diagnostic) {
	proj, diags := AnalyzeProject(files, opts)
	if names := proj.PackageNames(); len(names) > 0 {
		return proj.Packages[names[0]], diags
	}
	return newPackage(""), diags
}

// newPackage returns package name with empty symbol tables.
func newPackage(name string) *Package {
	return &Package{
		Name:        name,
		Types:       map[string]*ast.TypeDecl{},
		Enums:       map[string]*ast.EnumDecl{},
		Errors:      map[string]*ast.ErrorDecl{},
		Scalars:     map[string]*ast.ScalarDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
		Services:    map[string]*ServiceInfo{},
		Events:      map[string]*ast.EventDecl{},
	}
}

// newAnalyzer returns an analyzer with empty symbol tables for package name.
func newAnalyzer(proj *Project, name string, opts Options) *analyzer {
	return &analyzer{pkg: newPackage(name), proj: proj, opts: opts}
}

// runDeclPhase builds the symbol tables and merges services; every other
// phase reads them, so it runs first for every package.
func (a *analyzer) runDeclPhase(files []*ast.File) {
	a.collectDecls(files)
	a.mergeServices()
}

// runNamingPhase checks orphan extend blocks, declaration-name case and
// Go-name collisions.
func (a *analyzer) runNamingPhase(files []*ast.File) {
	a.checkExtendOrphans()
	a.checkDeclNameCase(files)
	a.checkFieldNameCollisions(files)
	a.checkEnumValueCollisions(files)
	a.checkDeclGoNameCollisions(files)
}

// runDecoratorPhase checks the decorators at every site and the JSON keys
// they give each body.
func (a *analyzer) runDecoratorPhase(files []*ast.File) {
	a.checkDecoratorSites(files)
	a.checkJSONKeys(files)
}

// runShapePhase checks the structural rules: field, enum, method, mixin,
// route and event shapes.
func (a *analyzer) runShapePhase(files []*ast.File) {
	a.checkFieldUniqueness()
	a.checkEnums()
	a.checkServiceMethods()
	a.checkFieldTypeCompat()
	a.checkRangesAndExtras(files)
	a.checkMixins()
	a.checkPathResolution()
	a.checkCombinationRules(files)
	a.checkFilePosition()
	a.checkEvents()
}

// runRefPhase checks each file's imports and every type reference.
func (a *analyzer) runRefPhase(files []*ast.File) {
	a.checkImports(files)
	a.checkTypeRefs(files)
}

// checkRangesAndExtras runs the field rules over every type and error body
// and the matching rules over every scalar.
func (a *analyzer) checkRangesAndExtras(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclRanges(d)
		}
	}
}

// checkDeclRanges runs the range rules for one declaration.
func (a *analyzer) checkDeclRanges(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkBodyRanges(dd.Body, dd.TypeParams)
	case *ast.ErrorDecl:
		a.checkBodyRanges(dd.Body, nil)
	case *ast.ScalarDecl:
		// Every field of the scalar's type inherits its constraints.
		a.checkValueRules(dd.Primitive, fmt.Sprintf("scalar %q", dd.Name), dd.Decorators)
	}
}

// checkBodyRanges runs the value rules and the field rules on each field of
// a body.
func (a *analyzer) checkBodyRanges(members []ast.TypeMember, typeParams []string) {
	for _, f := range ast.Fields(members) {
		a.checkValueRules(a.valuePrim(f), fmt.Sprintf("field %q", f.Name), f.Decorators)
		a.checkNullableRedundant(f)
		a.checkUniqueItemsComparable(f, typeParams)
		a.checkValueConstraintOnTypeParam(f, typeParams)
		a.checkMapKeyComparable(f, typeParams)
	}
}

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

type analyzer struct {
	pkg   *Package
	proj  *Project
	opts  Options
	diags []Diagnostic
}

// diag appends a diagnostic and returns a pointer into a.diags for setting
// Related; the pointer is invalid after the next append.
func (a *analyzer) diag(pos, end lexer.Position, sev lexer.Severity, code, format string, args ...any) *Diagnostic {
	a.diags = append(a.diags, Diagnostic{
		Pos:      pos,
		End:      end,
		Severity: sev,
		Code:     code,
		Msg:      fmt.Sprintf(format, args...),
	})
	return &a.diags[len(a.diags)-1]
}
