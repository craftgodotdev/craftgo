// Package semantic performs whole-package validation on parsed [ast.File]
// values and produces a merged, name-indexed [Package] for downstream tools.
//
// Responsibilities:
//
//   - Package-name consistency across files.
//   - Symbol tables for types, enums, errors, scalars, middlewares.
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
// Single-package [Analyze] uses a folder-merge import model and rejects
// qualified names; [AnalyzeProject] resolves cross-package qualified
// refs against the project's package set. Diagnostics carry stable
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
	// Middlewares maps `middleware Name(...)` declarations by name.
	Middlewares map[string]*ast.MiddlewareDecl
	// Services maps service names to the merged primary + extends bundle.
	Services map[string]*ServiceInfo
}

// ServiceInfo bundles the primary `service` declaration with every `extend
// service` continuation that targets the same name. Methods is the merged
// list in source order.
type ServiceInfo struct {
	Primary *ast.ServiceDecl
	Extends []*ast.ServiceDecl
	Methods []*ast.Method
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
	// design folder. When non-empty, [AnalyzeProject] splits files by
	// subdirectory into separate packages and resolves cross-package
	// qualified refs against each file's `import` declarations. When
	// empty (or when calling [Analyze] / [AnalyzeWith]) the analyser
	// behaves as a single-package merge.
	DesignRoot string

	// skipQualifiedRefCheck disables the in-package
	// [analyzer.checkQualifiedRefs] pass. Set internally by
	// [AnalyzeProject] when running per-package analysis - qualified
	// refs are validated by the project-level cross-package resolver
	// instead. Not exported: external callers should use
	// [AnalyzeProject] when they want this behaviour.
	skipQualifiedRefCheck bool

	// skipMiddlewareRefCheck disables the in-package middleware-ref
	// validation in [analyzer.checkDecoratorRefs]. Set internally by
	// [AnalyzeProject] so a `@middlewares(AuthRequired)` reference in
	// one package can resolve to a `middleware AuthRequired`
	// declaration in a sibling package without the per-package pass
	// reporting it as unknown first.
	skipMiddlewareRefCheck bool

	// skipExtendOrphanCheck disables the in-package orphan-extend
	// diagnostic. [AnalyzeProject] sets it so the project-level
	// resolver can produce a better message when the extended
	// service exists in a SIBLING package (a common typo source);
	// without the skip the per-package pass would fire first with
	// the generic "no primary declaration" message.
	skipExtendOrphanCheck bool

	// skipMixinCheck disables the in-package
	// [analyzer.checkMixins] pass so the project-level resolver
	// can run the unified mixin expansion with cross-package
	// scalars / enums / types in scope. Without the skip the
	// per-package pass silently swallows qualified mixin refs
	// (`shared.Timestamps`) and a cross-pkg field collision goes
	// undetected.
	skipMixinCheck bool

	// skipBindingTypeCheckQualified suppresses the per-package
	// binding-type check (`@path / @query / @header / @cookie /
	// @form` shape rules in [analyzer.checkBindingFieldType]) for
	// QUALIFIED refs only — bare names still resolve locally. The
	// per-package pass can't see another package's scalars / enums
	// so cross-pkg refs (`shared.Email @path`) would otherwise
	// false-reject. Project mode flips this on; the post-pass
	// [refResolver.checkProjectBindings] re-runs the check with
	// the full project symbol table.
	skipBindingTypeCheckQualified bool

	// skipPathParamCheck disables the per-package `@path` segment ↔
	// field check ([analyzer.checkMethodPathParams]). A request type
	// can embed a mixin from a SIBLING package whose fields supply the
	// path binding (`type Req { shared.IdHolder }`); the per-package
	// pass can't expand that mixin, so it would false-report the
	// segment as having no matching field. Project mode flips this on
	// and [refResolver.checkProjectPathParams] re-runs the check with
	// cross-package mixin resolution — matching what the codegen binder
	// already does via the project resolver.
	skipPathParamCheck bool
}

// Analyze validates the supplied AST files as a single package and returns
// the merged [Package] together with every diagnostic found. The Package
// value is always non-nil even when diagnostics were reported, so callers
// (codegen, LSP) can do best-effort downstream work.
//
// Equivalent to AnalyzeWith(files, [Options]{}).
func Analyze(files []*ast.File) (*Package, []Diagnostic) {
	return AnalyzeWith(files, Options{})
}

// AnalyzeWith is the [Analyze] variant that accepts cross-reference
// truth sources. CLI / codegen invocations supply the project's
// `craftgo.design.yaml` data here; the LSP either supplies the same
// (when it has read the manifest) or leaves it empty for syntax-only
// validation.
func AnalyzeWith(files []*ast.File, opts Options) (*Package, []Diagnostic) {
	a := &analyzer{
		pkg: &Package{
			Types:       map[string]*ast.TypeDecl{},
			Enums:       map[string]*ast.EnumDecl{},
			Errors:      map[string]*ast.ErrorDecl{},
			Scalars:     map[string]*ast.ScalarDecl{},
			Middlewares: map[string]*ast.MiddlewareDecl{},
			Services:    map[string]*ServiceInfo{},
		},
		opts: opts,
	}
	a.runDeclPhase(files)
	a.runNamingPhase(files)
	a.runDecoratorPhase(files)
	a.runShapePhase(files)
	a.runRefPhase(files)
	return a.pkg, a.diags
}

// runDeclPhase parses the AST into the package symbol tables and
// merges service primaries with their `extend` blocks. Every later
// phase reads the resulting tables, so this MUST run first.
func (a *analyzer) runDeclPhase(files []*ast.File) {
	a.checkPackageName(files)
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
	a.checkDeclNameCase(files)
	a.checkFieldNameCollisions(files)
	a.checkEnumValueCollisions(files)
	a.checkDeclGoNameCollisions(files)
}

// runDecoratorPhase covers every decorator-level rule: duplicates on
// the same site, placement against the registry, argument arity /
// type / value enums, and reference resolution to declared
// middlewares / errors / fields. Project-level cross-package
// references (middleware / security / errors) are gated by
// `skipMiddlewareRefCheck` because they need the full project symbol
// table — those run in [AnalyzeProject] after per-package analysis.
// LOCAL refs (field-group: `@requiresOneOf` / `@mutuallyExclusive`)
// always run — their targets are same-type fields, no cross-package
// resolution required, and skipping them silently allows typos like
// `@requiresOneOf(emial, phone)` to slip through to codegen.
func (a *analyzer) runDecoratorPhase(files []*ast.File) {
	a.checkDecoratorDuplicates(files)
	a.checkDecoratorPlacement(files)
	a.checkDecoratorArgs(files)
	a.checkDecoratorConflicts(files)
	a.checkLocalDecoratorRefs(files)
	if !a.opts.skipMiddlewareRefCheck {
		a.checkDecoratorRefs(files)
	}
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
	if !a.opts.skipMixinCheck {
		a.checkMixins()
	}
	a.checkGenerics()
	a.checkPathResolution()
	a.checkOperationIDUniqueness()
	a.checkCombinationRules(files)
}

// runRefPhase resolves every type reference - file-local imports,
// single-segment names against the package's symbol table, and
// qualified `pkg.Type` shapes against sibling packages.
// Cross-package qualified-ref validation is gated by
// `skipQualifiedRefCheck` so single-file LSP analysis (where
// sibling packages have not been loaded) does not over-report.
func (a *analyzer) runRefPhase(files []*ast.File) {
	a.checkImports(files)
	a.checkLocalTypeRefs(files)
	if !a.opts.skipQualifiedRefCheck {
		a.checkQualifiedRefs()
	}
}

// Diagnostic codes emitted by the semantic analyser. Stable identifiers
// so the LSP, docs site, and "disable next line" comments can reference
// individual rules. Group prefix (`decorator/`, `decl/`, `enum/`,
// `service/`, `field/`, `ref/`, `binding/`) lets the IDE collapse rules
// by topic; never reuse a string across groups.

type analyzer struct {
	pkg   *Package
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
