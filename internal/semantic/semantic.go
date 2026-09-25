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
	// Name is the declared package name; empty when no file declares one.
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
// project's only package, else the unnamed one, else the first by name. The
// package is never nil.
func Analyze(files []*ast.File) (*Package, []Diagnostic) {
	return analyzeWith(files, Options{})
}

// analyzeWith is [Analyze] with opts.
func analyzeWith(files []*ast.File, opts Options) (*Package, []Diagnostic) {
	proj, diags := AnalyzeProject(files, opts)
	return proj.singlePackage(), diags
}

// newPackage returns a package with empty symbol tables.
func newPackage() *Package {
	return &Package{
		Types:       map[string]*ast.TypeDecl{},
		Enums:       map[string]*ast.EnumDecl{},
		Errors:      map[string]*ast.ErrorDecl{},
		Scalars:     map[string]*ast.ScalarDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
		Services:    map[string]*ServiceInfo{},
		Events:      map[string]*ast.EventDecl{},
	}
}

// newAnalyzer returns an analyzer with empty symbol tables for one package.
func newAnalyzer(proj *Project, opts Options) *analyzer {
	return &analyzer{pkg: newPackage(), proj: proj, opts: opts}
}

// runDeclPhase builds the symbol tables and merges services; every other
// phase reads them, so it runs first for every package.
func (a *analyzer) runDeclPhase(files []*ast.File) {
	a.setPackageName(files)
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
