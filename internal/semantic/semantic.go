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
// Future work: cross-package qualified-ref resolution (v1 uses a
// folder-merge model and rejects qualified names), and richer path
// resolution against [config.OpenAPI].BasePath. Diagnostics carry
// stable [lexer.Diagnostic.Code] identifiers (`decorator/arity`,
// `mixin/conflict`, `generic/arity`, …) so the LSP and docs site can
// reference each rule individually.
package semantic

import (
	"fmt"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// Diagnostic re-exports [lexer.Diagnostic] so semantic-layer callers do not
// need to import the lexer package directly.
type Diagnostic = lexer.Diagnostic

// Package is the merged result of analysing one or more [ast.File] from the
// same logical package. The maps are keyed by the unqualified declaration
// name; cross-package references are resolved later (currently out of scope).
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
	// scheme name except the literal `noauth` must appear here or
	// produce a [CodeDecoratorRef] diagnostic.
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
// middlewares / errors / fields. Reference resolution is gated by
// `skipMiddlewareRefCheck` for the format-only LSP path that runs
// without a fully-resolved project.
func (a *analyzer) runDecoratorPhase(files []*ast.File) {
	a.checkDecoratorDuplicates(files)
	a.checkDecoratorPlacement(files)
	a.checkDecoratorArgs(files)
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
	a.checkMixins()
	a.checkGenerics()
	a.checkPathResolution()
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
const (
	// CodeDecoratorUnknown fires when `@name` is not in the registry.
	// Decorators are a closed set by design (no escape-hatch).
	CodeDecoratorUnknown = "decorator/unknown"
	// CodeDecoratorPlacement fires when a known decorator appears at a
	// site outside its declared [Spec.Levels].
	CodeDecoratorPlacement = "decorator/placement"
	// CodeDecoratorDuplicate fires when the same `@name` appears twice
	// in the same scope. Args do not disambiguate.
	CodeDecoratorDuplicate = "decorator/duplicate"
	// CodeDecoratorArity fires when the count of arguments to `@name`
	// is below ArgMin or above ArgMax.
	CodeDecoratorArity = "decorator/arity"
	// CodeDecoratorArgType fires when an argument literal kind does
	// not match the expected ArgKind for the position.
	CodeDecoratorArgType = "decorator/argtype"
	// CodeDecoratorArgValue fires when an argument value falls outside
	// the allowed enum set (e.g. `@format(garbage)`).
	CodeDecoratorArgValue = "decorator/argvalue"
	// CodeDecoratorRange fires when a numeric pair is out of order
	// (e.g. `@length(20, 5)`) or violates a per-decorator bound.
	CodeDecoratorRange = "decorator/range"
	// CodeDecoratorTypeMismatch fires when a validator decorator is
	// applied to an incompatible field/scalar primitive (e.g.
	// `@length` on `int`).
	CodeDecoratorTypeMismatch = "decorator/typemismatch"
	// CodeDecoratorRef fires when a decorator argument names an entity
	// (error / middleware / field / security scheme) that does not
	// exist in scope.
	CodeDecoratorRef = "decorator/ref"
	// CodeDecoratorRedundant fires when two decorators say the same
	// thing redundantly (warning, not error). Example: `@nullable`
	// on a `T?` field.
	CodeDecoratorRedundant = "decorator/redundant"

	// CodePackageMismatch fires when files disagree on the `package`
	// name.
	CodePackageMismatch = "decl/package-mismatch"
	// CodeDuplicateDecl fires when two top-level declarations share a
	// name across the merged package.
	CodeDuplicateDecl = "decl/duplicate"
	// CodeDeclNameCase fires (severity warning) when a top-level decl
	// - type / error / enum / service / middleware / scalar - does
	// not start with an uppercase letter. Lower-case decl names are
	// emitted verbatim by codegen, producing UNEXPORTED Go types
	// that cannot be imported across packages.
	CodeDeclNameCase = "decl/name-case"
	// CodeFieldNameCollision fires (severity warning) when two field
	// names in the same type / error body normalise to the same Go
	// identifier under [internal/idents.GoFieldName] (e.g. `user_id`
	// and `userId` both → `UserID`). Codegen still emits the struct
	// using `_2`, `_3`, ... suffixes so the result compiles, but
	// the JSON wire shape carries both DSL names verbatim - a quiet
	// schema duplication the user almost certainly did not intend.
	CodeFieldNameCollision = "field/name-collision"
	// CodeEnumValueCollision fires (severity warning) when two enum
	// values in the same enum normalise to the same Go const name
	// (e.g. `created` and `Created` both → `<Enum>Created`).
	// Codegen emits the trailing duplicates with `_2`, `_3`, ...
	// suffixes so the package compiles, but the wire payload
	// (string or int) of both values stays distinct - a quiet
	// duplication the user usually did not intend.
	CodeEnumValueCollision = "enum/value-collision"
	// CodeDeclGoNameCollision fires (severity ERROR) when two
	// top-level decls in the same package produce the same Go
	// identifier under codegen's name-mangling rules. Examples
	// caught by this rule:
	//
	//   - `type FooErr` + `error Foo` - both emit `type FooErr`
	//   - `type FooBody` + `error Foo { ... }` - both emit `type FooBody`
	//   - `type FooMiddleware` + `middleware Foo` - same
	//
	// Auto-suffixing decls would silently rename a symbol the user
	// references in their own Go code, so this is a hard error
	// rather than the soft warning used for FIELD-level dedup.
	CodeDeclGoNameCollision = "decl/go-name-collision"

	// CodeDuplicateField fires when two fields in the same type / error
	// body share a name.
	CodeDuplicateField = "field/duplicate"

	// CodeEnumDuplicateName fires for two enum values with the same
	// identifier.
	CodeEnumDuplicateName = "enum/duplicate-name"
	// CodeEnumMixedTypes fires when an enum mixes bare / int / string
	// values.
	CodeEnumMixedTypes = "enum/mixed-types"
	// CodeEnumDuplicateLiteral fires when two enum values share an
	// int or string literal.
	CodeEnumDuplicateLiteral = "enum/duplicate-literal"

	// CodeServiceDuplicate fires for two primary `service` decls of
	// the same name.
	CodeServiceDuplicate = "service/duplicate"
	// CodeServiceExtendOrphan fires when an `extend service` has no
	// primary declaration in the package.
	CodeServiceExtendOrphan = "service/extend-orphan"
	// CodeServiceExtendDecorators fires when an `extend service`
	// carries service-level decorators (those belong on the primary).
	CodeServiceExtendDecorators = "service/extend-decorators"
	// CodeServiceDuplicateMethod fires for two methods sharing a name
	// inside one service (after extends merge).
	CodeServiceDuplicateMethod = "service/duplicate-method"
	// CodeServiceDuplicateRoute fires for two methods sharing the
	// same VERB+path tuple (after extends merge).
	CodeServiceDuplicateRoute = "service/duplicate-route"

	// CodeBindingConflict fires when a field has more than one of
	// `@path / @query / @header / @cookie / @body / @form`.
	CodeBindingConflict = "binding/conflict"
	// CodeRequiredOptional fires when `@required` appears on a `T?`
	// field.
	CodeRequiredOptional = "binding/required-optional"
	// CodeBindingType fires when `@path`, `@header`, or `@cookie` is
	// applied to a field whose type is not a non-array, non-optional
	// `string`. The wire formats those decorators target carry only
	// strings (URL segments, header values, cookie values), and the
	// codegen would otherwise silently skip the field at gen time -
	// surfacing the mismatch at design time gives the author an
	// actionable error.
	CodeBindingType = "binding/type"
	// CodeServiceCollision fires when two packages in the same
	// project both declare a primary `service` of the same name.
	// The generated codegen layout keys output directories by
	// service name (`internal/routes/<svc>/`, `internal/handler/<svc>/`),
	// so a collision would silently overwrite one package's
	// scaffolds with the other's. Surface every conflicting
	// declaration so the author can rename one.
	CodeServiceCollision = "service/collision"
	// CodeMiddlewareCollision fires when two packages in the same
	// project both declare a `middleware` of the same name. Cross-
	// package middleware references are global by design, so a
	// collision would make `@middlewares(Name)` ambiguous - the
	// resolver picks the first match silently. The diagnostic
	// surfaces every conflicting declaration so the author can
	// rename or consolidate.
	CodeMiddlewareCollision = "middleware/collision"
	// CodePassthroughBody fires when a method tagged `@passthrough`
	// declares a `request` or `response` block. Passthrough endpoints
	// hand the raw `http.ResponseWriter` and `*http.Request` to logic;
	// any framework-side request/response shape would be ignored, so
	// the analyser rejects the mistake up front.
	CodePassthroughBody = "passthrough/has-body"

	// CodeQualifiedRef fires for a `pkg.Type` reference; v1 uses a
	// folder-merge import model and rejects qualified names.
	CodeQualifiedRef = "ref/qualified"

	// CodeMixinUnresolved fires when a mixin reference does not name
	// a type declared in the package.
	CodeMixinUnresolved = "mixin/unresolved"
	// CodeMixinNonType fires when a mixin reference resolves to a
	// non-type entity (enum, error, scalar, middleware).
	CodeMixinNonType = "mixin/non-type"
	// CodeMixinCycle fires when expanding a mixin would loop back
	// onto a type already on the expansion stack.
	CodeMixinCycle = "mixin/cycle"
	// CodeMixinConflict fires when expansion produces two fields
	// with the same name (mixin vs host or mixin vs mixin).
	CodeMixinConflict = "mixin/conflict"
	// CodeMixinArity fires when a generic mixin's argument count
	// disagrees with the target's TypeParams count.
	CodeMixinArity = "mixin/arity"

	// CodeGenericArity fires when a generic instance's argument count
	// disagrees with the target decl's TypeParams.
	CodeGenericArity = "generic/arity"
	// CodeGenericNonGeneric fires when a non-generic type is referenced
	// with `<...>` arguments.
	CodeGenericNonGeneric = "generic/non-generic"

	// CodePathBaseFormat warns when [Options.BasePath] is malformed -
	// missing leading slash, trailing slash, or contains `//`. Code-
	// gen normalises these so this is a warning, not an error.
	CodePathBaseFormat = "path/base-format"
	// CodePathCollision fires when two methods (across any service)
	// resolve to the same VERB + final-path tuple.
	CodePathCollision = "path/collision"
	// CodePathParamMissing fires when a `{name}` segment in the
	// resolved route has no corresponding field binding in the
	// method's request type.
	CodePathParamMissing = "path/param-missing"
	// CodePathParamOrphan fires when a request field uses `@path` /
	// `@path("name")` but the resolved route has no matching
	// `{name}` segment.
	CodePathParamOrphan = "path/param-orphan"
	// CodePathHealthConflict fires when a user-declared method's
	// resolved route equals one of the runtime-reserved health paths
	// (`/healthz`, `/readyz` by default).
	CodePathHealthConflict = "path/health-conflict"

	// CodeImportUnresolved fires when `import "path"` does not
	// correspond to a folder under the design root.
	CodeImportUnresolved = "import/unresolved"
	// CodeImportEscape fires when an import path uses `..` or starts
	// with `/` to escape the design root.
	CodeImportEscape = "import/escape"
	// CodeImportDuplicate fires when one file imports the same path
	// twice (with or without matching aliases) - a clear redundancy
	// the parser cannot detect without per-file context.
	CodeImportDuplicate = "import/duplicate"
	// CodeImportAliasConflict fires when two imports in the same
	// file resolve to the same alias (explicit or implicit), making
	// later qualified references like `alias.Type` ambiguous.
	CodeImportAliasConflict = "import/alias-conflict"
	// CodeImportSelf fires when a file imports a folder whose files
	// share its own `package X` declaration - the import is a no-op
	// since the analyser already merges them by package name.
	CodeImportSelf = "import/self"
	// CodeRefUnknownPackage fires when `pkg.Type` references a
	// package whose `package X` declaration doesn't appear anywhere
	// in the project.
	CodeRefUnknownPackage = "ref/unknown-package"
	// CodeRefUnknownSymbol fires when the package resolves correctly
	// but doesn't declare the named type.
	CodeRefUnknownSymbol = "ref/unknown-symbol"
)

// related is a tiny helper that builds a single-element [lexer.Related]
// slice. Most semantic diagnostics link to exactly one prior site (a
// duplicate's first occurrence, a binding's first decorator, etc.); the
// helper keeps the call site readable.
func related(pos lexer.Position, msg string) []lexer.Related {
	return []lexer.Related{{Pos: pos, Msg: msg}}
}

// decoratorEnd returns the half-open end position covering `@name`, used
// as the [Diagnostic.End] for placement / unknown errors. We don't have
// the exact closing-paren position in the AST, so the range covers just
// the `@name` token - enough for LSP to underline the offending
// decorator without spilling into argument literals.
func decoratorEnd(d *ast.Decorator) lexer.Position {
	end := d.Pos
	// +1 for the leading '@', +len(Name) for the identifier itself.
	w := 1 + len(d.Name)
	end.Column += w
	end.Offset += w
	return end
}

// checkDecoratorPlacement validates every decorator against the registry
// and the README compatibility matrix. Two distinct diagnostics fire:
//
//   - [CodeDecoratorUnknown] when the name is not registered. craftgo
//     treats the decorator set as closed; an unknown name is almost
//     always a typo (`@deprecate` vs `@deprecated`).
//   - [CodeDecoratorPlacement] when the name is registered but the
//     current site is not in its allowed [Spec.Levels].
//
// The check is independent of duplicate / combination rules above - a
// decorator that is both duplicate and misplaced gets two separate
// diagnostics, each with its own Code so the IDE can group them.
func (a *analyzer) checkDecoratorPlacement(files []*ast.File) {
	for _, f := range files {
		a.checkPlacement(LvlFile, "file", f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclPlacement(d)
		}
	}
}

// checkDeclPlacement dispatches placement checks for one top-level
// declaration plus every nested scope it owns.
func (a *analyzer) checkDeclPlacement(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkPlacement(LvlType, "type "+dd.Name, dd.Decorators)
		a.checkFieldPlacement(LvlField, dd.Name, dd.Body)
	case *ast.EnumDecl:
		a.checkPlacement(LvlEnum, "enum "+dd.Name, dd.Decorators)
		for _, v := range dd.Values {
			a.checkPlacement(LvlEnumValue, "enum value "+dd.Name+"."+v.Name, v.Decorators)
		}
	case *ast.ErrorDecl:
		a.checkPlacement(LvlError, "error "+dd.Name, dd.Decorators)
		// Error bodies are server-emitted, so binding decorators
		// (`@path`, `@query`, ...) and input-validators (`@minLength`,
		// `@pattern`, ...) are rejected via the narrower
		// [LvlErrorField] site.
		a.checkFieldPlacement(LvlErrorField, dd.Name, dd.Body)
	case *ast.ScalarDecl:
		a.checkPlacement(LvlScalar, "scalar "+dd.Name, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkPlacement(LvlMiddleware, "middleware "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		// `extend service` cannot carry service-level decorators (rejected
		// by [mergeServices]); we still walk methods so placement on
		// extended methods is checked.
		if !dd.Extend {
			a.checkPlacement(LvlService, "service "+dd.Name, dd.Decorators)
		}
		for _, m := range dd.Methods {
			a.checkPlacement(LvlMethod, "method "+dd.Name+"."+m.Name, m.Decorators)
		}
	}
}

// checkFieldPlacement applies the placement check to every Field in a
// type or error body. Mixin members carry no decorators and are skipped.
// site is [LvlField] for type bodies and [LvlErrorField] for error
// bodies - the latter rejects request-binding and input-validator
// decorators that don't make sense on server-emitted payloads.
func (a *analyzer) checkFieldPlacement(site Level, parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkPlacement(site, site.Name()+" "+parent+"."+f.Name, f.Decorators)
	}
}

// checkPlacement is the leaf: for every decorator in decs, look up the
// registry and emit `decorator/unknown` or `decorator/placement` as
// appropriate. site is the bit for the current declaration site;
// scopeLabel is a human-readable phrase for the diagnostic message
// (e.g. "field User.name").
//
// Nil entries are tolerated for symmetry with [checkDecoratorScope] -
// the parser doesn't produce them today but the defensive guard keeps a
// future regression from crashing the analyser.
func (a *analyzer) checkPlacement(site Level, scopeLabel string, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		spec, known := Lookup(d.Name)
		if !known {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorUnknown,
				"unknown decorator @%s on %s (not in the framework registry)", d.Name, scopeLabel)
			continue
		}
		if spec.Levels&site == 0 {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorPlacement,
				"@%s is not allowed on %s; valid sites: %s", d.Name, scopeLabel, spec.Levels)
		}
	}
}

// analyzer is the per-call state of [Analyze]. Kept private to discourage
// callers from introspecting partial results.
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

// checkPackageName ensures every file with a `package` declaration agrees on
// the same package name and stores it on a.pkg.Name. Files without an
// explicit `package` are treated as belonging to whatever name the others
// pick.
func (a *analyzer) checkPackageName(files []*ast.File) {
	var name string
	var firstPos lexer.Position
	for _, f := range files {
		if f.Package == nil {
			continue
		}
		if name == "" {
			name = f.Package.Name
			firstPos = f.Package.Pos
			continue
		}
		if name != f.Package.Name {
			d := a.diag(f.Package.Pos, f.Package.Pos, lexer.SeverityError,
				CodePackageMismatch,
				"package name %q conflicts with %q", f.Package.Name, name)
			d.Related = related(firstPos, "first declared here")
		}
	}
	a.pkg.Name = name
}

// collectDecls walks every declaration once, populates the Package symbol
// tables, and reports duplicate top-level names. Services are special-cased:
// they merge across files via [ServiceInfo] (see [mergeServices]).
//
// Namespace separation matches the codegen output packages:
//
//   - type / enum / scalar / error → all emit into the types package,
//     so they share one `seen` map. A duplicate name across kinds is
//     a hard collision in the generated Go.
//   - middleware → emits into its own package (svccontext aliases +
//     middleware impl pkg), independent from types. Uses a separate
//     `seenMW` map so `middleware Foo` and `type Foo` coexist.
//   - service → handler / route packages, each namespaced per
//     service; merge handled by mergeServices.
func (a *analyzer) collectDecls(files []*ast.File) {
	seen := map[string]lexer.Position{}    // type / enum / scalar / error namespace
	seenMW := map[string]lexer.Position{} // middleware namespace
	registerIn := func(table map[string]lexer.Position, name string, pos lexer.Position) bool {
		if prev, ok := table[name]; ok {
			d := a.diag(pos, pos, lexer.SeverityError, CodeDuplicateDecl,
				"duplicate top-level declaration %q", name)
			d.Related = related(prev, "first declared here")
			return false
		}
		table[name] = pos
		return true
	}
	for _, f := range files {
		for _, d := range f.Decls {
			// Defensive: the parser is supposed to drop typed-nil
			// pointers before they reach the AST, but mid-typing
			// edits in the LSP have surfaced regressions before - a
			// nil decl here used to crash the entire analyse. Skip
			// instead.
			if d == nil {
				continue
			}
			switch dd := d.(type) {
			case *ast.TypeDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos) {
					a.pkg.Types[dd.Name] = dd
				}
			case *ast.EnumDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos) {
					a.pkg.Enums[dd.Name] = dd
				}
			case *ast.ErrorDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos) {
					a.pkg.Errors[dd.Name] = dd
				}
			case *ast.ScalarDecl:
				if dd == nil {
					continue
				}
				if registerIn(seen, dd.Name, dd.Pos) {
					a.pkg.Scalars[dd.Name] = dd
				}
			case *ast.MiddlewareDecl:
				if dd == nil {
					continue
				}
				if registerIn(seenMW, dd.Name, dd.Pos) {
					a.pkg.Middlewares[dd.Name] = dd
				}
			case *ast.ServiceDecl:
				if dd == nil {
					continue
				}
				si, ok := a.pkg.Services[dd.Name]
				if !ok {
					si = &ServiceInfo{}
					a.pkg.Services[dd.Name] = si
				}
				if dd.Extend {
					si.Extends = append(si.Extends, dd)
				} else if si.Primary != nil {
					d := a.diag(dd.Pos, dd.Pos, lexer.SeverityError, CodeServiceDuplicate,
						"duplicate primary service %q", dd.Name)
					d.Related = related(si.Primary.Pos, "first declared here")
				} else {
					si.Primary = dd
				}
			}
		}
	}
}

// mergeServices flattens each [ServiceInfo] into a single ordered method
// list. `extend service` declarations may not carry service-level
// decorators (those belong on the primary), and orphan extends without a
// primary are reported.
func (a *analyzer) mergeServices() {
	for name, si := range a.pkg.Services {
		if si.Primary == nil {
			if !a.opts.skipExtendOrphanCheck {
				for _, e := range si.Extends {
					a.diag(e.Pos, e.Pos, lexer.SeverityError, CodeServiceExtendOrphan,
						"extend service %q has no primary declaration", name)
				}
			}
			continue
		}
		si.Methods = append(si.Methods, si.Primary.Methods...)
		for _, e := range si.Extends {
			if len(e.Decorators) > 0 {
				d := a.diag(e.Pos, e.Pos, lexer.SeverityError, CodeServiceExtendDecorators,
					"extend service %q must not have service-level decorators", name)
				d.Related = related(si.Primary.Pos, "primary service declared here")
			}
			si.Methods = append(si.Methods, e.Methods...)
		}
	}
}

// checkFieldUniqueness enforces that no type or error body has two fields
// with the same name. Mixin members are skipped here - their fields are
// expanded later (when implemented) and clash detection happens then.
func (a *analyzer) checkFieldUniqueness() {
	check := func(name string, members []ast.TypeMember) {
		seen := map[string]lexer.Position{}
		for _, m := range members {
			f, ok := m.(*ast.Field)
			if !ok {
				continue
			}
			if prev, exists := seen[f.Name]; exists {
				d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateField,
					"duplicate field %q in %q", f.Name, name)
				d.Related = related(prev, "first declared here")
			} else {
				seen[f.Name] = f.Pos
			}
		}
	}
	for _, td := range a.pkg.Types {
		check(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		check(ed.Name, ed.Body)
	}
}

// checkEnums verifies (a) value-name uniqueness inside each enum, (b)
// uniform value kind across the enum, and (c) literal-value uniqueness for
// int and string enums.
func (a *analyzer) checkEnums() {
	for _, ed := range a.pkg.Enums {
		seenNames := map[string]lexer.Position{}
		seenInts := map[int64]lexer.Position{}
		seenStrs := map[string]lexer.Position{}
		var firstKind ast.EnumValueKind
		var firstKindPos lexer.Position
		first := true
		for _, v := range ed.Values {
			if prev, dup := seenNames[v.Name]; dup {
				d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumDuplicateName,
					"duplicate enum value name %q in %q", v.Name, ed.Name)
				d.Related = related(prev, "first declared here")
			}
			seenNames[v.Name] = v.Pos
			if first {
				firstKind = v.Kind
				firstKindPos = v.Pos
				first = false
			} else if v.Kind != firstKind {
				d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumMixedTypes,
					"enum %q has mixed value types (must be all bare, all int, or all string)", ed.Name)
				d.Related = related(firstKindPos, "first value declared here")
			}
			switch v.Kind {
			case ast.EnumInt:
				if prev, dup := seenInts[v.IntValue]; dup {
					d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumDuplicateLiteral,
						"duplicate int value %d in enum %q", v.IntValue, ed.Name)
					d.Related = related(prev, "first used here")
				}
				seenInts[v.IntValue] = v.Pos
			case ast.EnumString:
				if prev, dup := seenStrs[v.StrValue]; dup {
					d := a.diag(v.Pos, v.Pos, lexer.SeverityError, CodeEnumDuplicateLiteral,
						"duplicate string value %q in enum %q", v.StrValue, ed.Name)
					d.Related = related(prev, "first used here")
				}
				seenStrs[v.StrValue] = v.Pos
			}
		}
	}
}

// checkServiceMethods enforces method-name and route-key uniqueness within
// each service after extends have been merged.
func (a *analyzer) checkServiceMethods() {
	for _, si := range a.pkg.Services {
		seenName := map[string]lexer.Position{}
		seenRoute := map[string]lexer.Position{}
		for _, m := range si.Methods {
			if prev, ok := seenName[m.Name]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateMethod,
					"duplicate method %q", m.Name)
				d.Related = related(prev, "first declared here")
			} else {
				seenName[m.Name] = m.Pos
			}
			key := m.Verb + " " + PathString(m.Path)
			if prev, ok := seenRoute[key]; ok {
				d := a.diag(m.Pos, m.Pos, lexer.SeverityError, CodeServiceDuplicateRoute,
					"duplicate route %q", key)
				d.Related = related(prev, "first declared here")
			} else {
				seenRoute[key] = m.Pos
			}
		}
	}
}

// checkDecoratorDuplicates rejects two `@same` decorators in the same
// declaration scope. Decorators are identified by their bare name; arguments
// don't disambiguate (`@tags("a")` + `@tags("b")` is still a duplicate). The
// second occurrence is reported, pointing back at the first for context. We
// walk every scope that can carry decorators: the file header, top-level
// declarations, fields inside type / error bodies, enum values, service
// methods, and middleware-declaration sites.
func (a *analyzer) checkDecoratorDuplicates(files []*ast.File) {
	for _, f := range files {
		a.checkDecoratorScope("file", f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclDecorators(d)
		}
	}
}

// checkDeclDecorators dispatches decorator-uniqueness checks for one
// top-level declaration plus every nested scope it owns (fields, methods,
// enum values).
func (a *analyzer) checkDeclDecorators(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkDecoratorScope("type "+dd.Name, dd.Decorators)
		a.checkFieldDecorators(dd.Name, dd.Body)
	case *ast.EnumDecl:
		a.checkDecoratorScope("enum "+dd.Name, dd.Decorators)
		for _, v := range dd.Values {
			a.checkDecoratorScope("enum value "+dd.Name+"."+v.Name, v.Decorators)
		}
	case *ast.ErrorDecl:
		a.checkDecoratorScope("error "+dd.Name, dd.Decorators)
		a.checkFieldDecorators(dd.Name, dd.Body)
	case *ast.ScalarDecl:
		a.checkDecoratorScope("scalar "+dd.Name, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkDecoratorScope("middleware "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		scope := "service " + dd.Name
		if dd.Extend {
			scope = "extend " + scope
		}
		a.checkDecoratorScope(scope, dd.Decorators)
		for _, m := range dd.Methods {
			a.checkDecoratorScope("method "+dd.Name+"."+m.Name, m.Decorators)
		}
	}
}

// checkFieldDecorators applies the duplicate check to every Field in a type
// or error body. Mixin members carry no decorators and are skipped.
func (a *analyzer) checkFieldDecorators(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkDecoratorScope("field "+parent+"."+f.Name, f.Decorators)
	}
}

// checkDecoratorScope is the leaf check: emit a diagnostic for any decorator
// whose Name appears more than once in decs. The first occurrence is silent;
// every subsequent one is flagged with a Related link to the first so the
// IDE can render a clickable cross-reference.
func (a *analyzer) checkDecoratorScope(scope string, decs []*ast.Decorator) {
	seen := map[string]lexer.Position{}
	for _, d := range decs {
		if d == nil {
			continue
		}
		if prev, ok := seen[d.Name]; ok {
			diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorDuplicate,
				"duplicate decorator @%s on %s", d.Name, scope)
			diag.Related = related(prev, "first decorator here")
			continue
		}
		seen[d.Name] = d.Pos
	}
}

// checkQualifiedRefs flags any `pkg.Type` reference that the v1 model
// cannot resolve. CraftGo v1 uses a folder-merge import model: every
// `.craftgo` file reachable from the design root contributes to a single
// logical package, so type references should be unqualified. A multi-part
// qualified name (e.g. `shared.User`) parses successfully - the AST keeps it
// so the v2 cross-package resolver has something to work with - but produces
// a Go compile error downstream because no Go-level import is emitted. We
// turn that latent failure into a friendly diagnostic up front.
//
// Mixin references are exempt from the check because the codegen already
// strips qualified prefixes (`emitMixin` uses the trailing segment) and the
// generated Go has no other way to spell the embedded field name.
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
//   - `@required` cannot coexist with `T?` (an optional type - they
//     contradict each other).
//   - At most one of `@path / @query / @header / @cookie / @body / @form`
//     may appear on a single field; multiple non-body bindings would
//     compete for the same value at runtime.
//   - `@passthrough` methods may not declare `request` or `response` -
//     logic handles the wire format directly, so any framework-managed
//     shape would be silently ignored.
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
		for _, m := range dd.Methods {
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
		a.checkRequiredOptional(parent, f)
		a.checkSingleBinding(parent, f)
		a.checkBindingFieldType(parent, f)
	}
}

// checkBindingFieldType rejects `@path`, `@header`, and `@cookie` on a
// field whose type is not a non-array, non-optional `string`. Those
// wire formats carry only strings - the codegen would otherwise
// silently skip the field, leaving the author with a runtime gap that
// no diagnostic explains. We raise the error at design time so the
// fix lands in the DSL where the mistake was made.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field) {
	if f.Type == nil {
		return
	}
	stringOnly := map[string]bool{"path": true, "header": true, "cookie": true}
	for _, d := range f.Decorators {
		if !stringOnly[d.Name] {
			continue
		}
		if isPlainString(f.Type) {
			continue
		}
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
			"field %s.%s: @%s requires a non-array, non-optional string field - got %s",
			parent, f.Name, d.Name, describeTypeRef(f.Type))
		return
	}
}

// isPlainString reports whether t is a non-array, non-optional `string`.
// Mirrors codegen's `isPlainStringField` so the design-time check and
// the codegen accept the same shape.
func isPlainString(t *ast.TypeRef) bool {
	if t == nil || t.Array || t.Optional {
		return false
	}
	return t.Named != nil && t.Named.Name.String() == "string"
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

// checkRequiredOptional rejects `@required` on a `T?` field. The two
// say opposite things ("must be present" vs "may be absent"), and
// silently ignoring one would let a buggy validator pass.
func (a *analyzer) checkRequiredOptional(parent string, f *ast.Field) {
	if f.Type == nil || !f.Type.Optional {
		return
	}
	for _, d := range f.Decorators {
		if d.Name == "required" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeRequiredOptional,
				"field %s.%s: @required is incompatible with optional type %q (drop one)",
				parent, f.Name, "T?")
			return
		}
	}
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

// PathString renders an [ast.Path] as a printable route string, e.g.
// `/users/{id}/posts`. A nil path renders as the empty string. Used for
// route-collision detection and diagnostics.
//
// Hot path (called per route during collision detection): Builder
// keeps the per-segment append allocation-free.
func PathString(p *ast.Path) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteByte('{')
			sb.WriteString(s.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}
