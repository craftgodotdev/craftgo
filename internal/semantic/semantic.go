// Package semantic performs whole-package validation on parsed [ast.File]
// values and produces a merged, name-indexed [Package] for downstream tools.
//
// Responsibilities:
//
//   - Package-name consistency across files.
//   - Symbol tables for types, enums, errors, scalars, middlewares,
//     events, consumers.
//   - Primary / `extend service` merge.
//   - Duplicate names (top-level, fields, methods, routes) and
//     uniform enum value kinds.
//   - Decorator placement, arity, argument literal types, value-set
//     enums, cross-references (errors / middlewares / security
//     schemes / requiresOneOf field idents), and value-range checks.
//   - Field-type compatibility for validator decorators (string
//     validators only on strings, etc.).
//   - Mixin field expansion: cycle, conflict, and generic-arity
//     detection.
//   - Generic instantiation: arg arity, non-generic-with-args, and
//     type-parameter scoping.
//
// [AnalyzeProject] groups files by their `package X` declaration and
// resolves cross-package qualified refs against the project's package
// set; [Analyze] is the single-package convenience over it. Diagnostics carry stable
// [lexer.Diagnostic.Code] identifiers (`decorator/arity`,
// `mixin/conflict`, `generic/arity`, …) so the LSP and docs site can
// reference each rule individually.
package semantic

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Diagnostic re-exports [lexer.Diagnostic] so semantic-layer callers do not
// need to import the lexer package directly.
type Diagnostic = lexer.Diagnostic

// Package is the merged result of analysing one or more [ast.File] from the
// same logical package. The maps are keyed by the unqualified declaration
// name; cross-package references are resolved by [AnalyzeProject].
type Package struct {
	// Name is the package name agreed on by every file with a `package`
	// declaration. Empty when no file has one.
	Name string
	// Types maps `type Name { ... }` declarations by name.
	Types map[string]*ast.TypeDecl
	// Enums maps `enum Name { ... }` declarations by name.
	Enums map[string]*ast.EnumDecl
	// Errors maps `error Cat Name [{ ... }]` declarations by name.
	Errors map[string]*ast.ErrorDecl
	// Scalars maps `scalar Name Primitive` declarations by name.
	Scalars map[string]*ast.ScalarDecl
	// Middlewares maps `middleware Name` declarations by name.
	Middlewares map[string]*ast.MiddlewareDecl
	// ConsumeMiddlewares maps `consume middleware Name` declarations by
	// name. The two tables are separate so a name resolves to the shape
	// its site needs; one name may appear in only one of them.
	ConsumeMiddlewares map[string]*ast.MiddlewareDecl
	// Services maps service names to the merged primary + extends bundle.
	Services map[string]*ServiceInfo
	// Events maps `event Name { ... }` declarations by name. Events have
	// their own namespace, so an event and its payload type may share a
	// name.
	Events map[string]*EventInfo
	// Consumers maps `consume Name { ... }` declarations by name, in
	// their own namespace for the same reason.
	Consumers map[string]*ConsumerInfo
}

// ServiceInfo bundles the primary `service` declaration with every `extend
// service` continuation that targets the same name. Methods, Events and
// Consumers are the merged lists in source order.
type ServiceInfo struct {
	Primary   *ast.ServiceDecl
	Extends   []*ast.ServiceDecl
	Methods   []*ast.Method
	Events    []*ast.EventDecl
	Consumers []*ast.ConsumerDecl
}

// Options configure the analyser's optional cross-reference checks.
// Pass an empty Options for the default (no truth source for security
// schemes); the corresponding refs are then silently allowed.
type Options struct {
	// SecuritySchemes lists names declared in the OpenAPI manifest
	// (`craftgo.design.yaml` openapi.securitySchemes). When nil the
	// `@security(name)` reference check is skipped - there is no
	// authoritative list to compare against. When non-nil, every
	// scheme name must appear here or produce a [CodeDecoratorRef]
	// diagnostic. To opt out of inherited security on a public
	// endpoint use `@ignoreSecurity` (not a sentinel scheme name).
	SecuritySchemes []string

	// BasePath is the project's `openapi.basePath` from the manifest.
	// Used by the path-resolution pass to compute final routes for
	// cross-service collision detection and to surface `path/format`
	// warnings on a malformed value. Empty disables basePath checks
	// (as if no basePath were declared).
	BasePath string

	// HealthPaths overrides the default `/healthz`, `/readyz` reserved
	// path set. Empty slice = default; nil also = default. A
	// user-declared method matching one of these paths produces a
	// `path/health-conflict` diagnostic.
	HealthPaths []string

	// DesignRoot is the absolute filesystem path of the project's
	// design folder, used to check `import "path"` declarations against
	// the filesystem. When empty the import paths are not checked.
	DesignRoot string

	// FileCase is the project's `output.fileCase` from the manifest -
	// "kebab", "snake" or "camel". It decides the directory name an
	// UNGROUPED service occupies, which the group-collision check
	// compares against every declared `@group`. Empty means the config
	// default (snake), matching the value craftgo.design.yaml resolves to
	// before codegen runs, so the analyser and the emitters agree on the
	// layout.
	FileCase string
}

// Analyze validates files as a project and returns the package they
// declare together with every diagnostic found. The Package value is
// always non-nil even when diagnostics were reported, so callers
// (codegen, LSP) can do best-effort downstream work.
//
// Equivalent to AnalyzeWith(files, [Options]{}).
func Analyze(files []*ast.File) (*Package, []Diagnostic) {
	return AnalyzeWith(files, Options{})
}

// AnalyzeWith is the [Analyze] variant that accepts cross-reference
// truth sources. It runs [AnalyzeProject] and returns the project's
// single package - the named package when exactly one is declared, the
// unnamed bucket otherwise.
func AnalyzeWith(files []*ast.File, opts Options) (*Package, []Diagnostic) {
	proj, diags := AnalyzeProject(files, opts)
	return proj.singlePackage(), diags
}

// newAnalyzer returns an analyzer with empty symbol tables for one package.
func newAnalyzer(proj *Project, opts Options) *analyzer {
	return &analyzer{
		pkg: &Package{
			Types:              map[string]*ast.TypeDecl{},
			Enums:              map[string]*ast.EnumDecl{},
			Errors:             map[string]*ast.ErrorDecl{},
			Scalars:            map[string]*ast.ScalarDecl{},
			Middlewares:        map[string]*ast.MiddlewareDecl{},
			ConsumeMiddlewares: map[string]*ast.MiddlewareDecl{},
			Services:           map[string]*ServiceInfo{},
			Events:             map[string]*EventInfo{},
			Consumers:          map[string]*ConsumerInfo{},
		},
		proj: proj,
		opts: opts,
	}
}

// runDeclPhase parses the AST into the package symbol tables and
// merges service primaries with their `extend` blocks. Every later
// phase reads the resulting tables, so this MUST run first.
func (a *analyzer) runDeclPhase(files []*ast.File) {
	a.setPackageName(files)
	a.collectDecls(files)
	a.mergeServices()
}

// runNamingPhase enforces naming conventions and detects collisions
// before any decorator / shape pass runs. Lower-case decl names,
// case-flip field collisions, enum-value collisions, and
// suffix-mangled cross-decl Go-name collisions all surface here.
// Order: decl-name case before any field/enum collision so the
// IDE squiggle highlights the spelling that needs fixing first.
func (a *analyzer) runNamingPhase(files []*ast.File) {
	a.checkExtendOrphans()
	a.checkDeclNameCase(files)
	a.checkFieldNameCollisions(files)
	a.checkEnumValueCollisions(files)
	a.checkDeclGoNameCollisions(files)
}

// runDecoratorPhase covers every decorator-level rule: duplicates on
// the same site, placement against the registry, argument arity /
// type / value enums, and reference resolution to declared
// middlewares / errors / security schemes / fields.
func (a *analyzer) runDecoratorPhase(files []*ast.File) {
	a.checkDecoratorDuplicates(files)
	a.checkDecoratorPlacement(files)
	a.checkDecoratorArgs(files)
	a.checkDecoratorConflicts(files)
	a.checkLocalDecoratorRefs(files)
	a.checkDecoratorRefs(files)
}

// runShapePhase covers the structural rules - uniqueness, enum
// shape, service-method shape, field-type compatibility, range
// ordering, mixin expansion, generic instantiation, path
// resolution. These rules consume the symbol tables built in
// [runDeclPhase] and the decorator metadata validated in
// [runDecoratorPhase], so they run after both.
func (a *analyzer) runShapePhase(files []*ast.File) {
	a.checkFieldUniqueness()
	a.checkEnums()
	a.checkServiceMethods()
	a.checkFieldTypeCompat()
	a.checkRangesAndExtras(files)
	a.checkMixins()
	a.checkGenerics()
	a.checkPathResolution()
	a.checkCombinationRules(files)
	a.checkFilePosition()
	a.checkEvents()
}

// runRefPhase validates file-local imports and resolves every
// single-segment type name against the package's symbol table;
// qualified `pkg.Type` references resolve in the project pass.
func (a *analyzer) runRefPhase(files []*ast.File) {
	a.checkImports(files)
	a.checkLocalTypeRefs(files)
}

type analyzer struct {
	pkg   *Package
	proj  *Project
	opts  Options
	diags []Diagnostic
}

// diag appends a fully-structured diagnostic. End may be equal to pos
// when only the start point is known - the LSP layer renders that as a
// single-column underline. The returned pointer aliases the slot inside
// a.diags so the caller can attach Related links inline; do not retain
// the pointer past the next a.diag call (slice growth invalidates it).
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
