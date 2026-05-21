// Diagnostic code constants + helper builders.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

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
	// CodeDecoratorConflict fires when two decorators on the same site
	// have semantics that contradict. Example: `@sensitive` paired with
	// a wire-shaping validator like `@length` (sensitive fields never
	// cross the wire so wire-level constraints are meaningless).
	CodeDecoratorConflict = "decorator/conflict"
	// CodeFlagEmptyParens fires (severity warning) when a Flag
	// decorator (one that never takes arguments) is written with empty
	// parens — `@positive()` instead of `@positive`. Warning only:
	// `craftgo fmt` strips the parens on save so canonical form is
	// parens-free.
	CodeFlagEmptyParens = "decorator/flag-empty-parens"
	// CodeArgPreferIdent fires (severity warning) when a decorator
	// argument names a registered identifier (format name, security
	// scheme, ...) but the source spells it as a quoted string. The
	// canonical form is bare ident — `@format(email)` not
	// `@format("email")`. `craftgo fmt` rewrites on save.
	CodeArgPreferIdent = "decorator/arg-prefer-ident"
	// CodeBoundOverflow fires when a numeric bound literal exceeds
	// the field type's capacity. `int8 @lte(300)` — 300 overflows
	// int8 (max 127). Without this check codegen emits an untyped
	// integer literal that fails to compile against the typed field.
	CodeBoundOverflow = "decorator/bound-overflow"
	// CodeBoundEmptyRange fires when two comparison decorators on the
	// same field define an empty value set. `@gt(5) @lt(5)`,
	// `@gte(N) @lt(N)`, and `@gt(N) @lte(N)` all reject every value.
	CodeBoundEmptyRange = "decorator/empty-range"
	// CodeMutExSingleField fires when `@mutuallyExclusive` is given
	// fewer than 2 fields. The runtime check (`n > 1`) is provably
	// unreachable.
	CodeMutExSingleField = "decorator/single-field-mutex"
	// CodeDuplicateGroupField fires when a cross-field validator
	// (`@requiresOneOf`, `@mutuallyExclusive`) lists the same field
	// name twice. Codegen would emit `v.A == nil && v.A == nil` which
	// `go vet` rejects as a redundant boolean expression.
	CodeDuplicateGroupField = "decorator/duplicate-group-field"

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
	// CodeEnumEmpty fires when an `enum X { }` has zero values. An
	// empty enum has no value that passes validation and emits
	// `enum: []` which violates JSON Schema 2020-12.
	CodeEnumEmpty = "enum/empty-values"

	// CodeServiceDuplicate fires for two primary `service` decls of
	// the same name.
	CodeServiceDuplicate = "service/duplicate"
	// CodeServiceExtendOrphan fires when an `extend service` has no
	// primary declaration in the package.
	CodeServiceExtendOrphan = "service/extend-orphan"
	// CodeExtendDecoratorNotMethod fires when an `extend service` block
	// carries a decorator that has no method-level form (e.g. `@prefix`).
	// Such decorators must sit on the primary service declaration;
	// putting them on an extend block would propagate to every method
	// in the block, which is meaningless for service-only directives.
	CodeExtendDecoratorNotMethod = "service/extend-decorator-not-method"
	// CodeServiceDuplicateMethod fires for two methods sharing a name
	// inside one service (after extends merge).
	CodeServiceDuplicateMethod = "service/duplicate-method"
	// CodeServiceDuplicateRoute fires for two methods sharing the
	// same VERB+path tuple (after extends merge).
	CodeServiceDuplicateRoute = "service/duplicate-route"

	// CodeBindingConflict fires when a field has more than one of
	// `@path / @query / @header / @cookie / @body / @form`.
	CodeBindingConflict = "binding/conflict"
	// CodeDefaultNeedsOptional fires (severity Warning) when a field
	// carries `@default(...)` but its type lacks the `?` suffix. The
	// formatter auto-adds `?` on save, so the warning clears as soon
	// as the user runs `craftgo fmt` (or format-on-save).
	CodeDefaultNeedsOptional = "decorator/default-needs-optional"
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

	// CodeQualifiedRef fires for a `pkg.Type` reference. The current
	// resolver uses folder-merge imports and rejects qualified names.
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
	// CodeScalarBadPrimitive fires when a `scalar Name Primitive`
	// declaration uses a non-builtin word in the primitive slot
	// (e.g. another type name, a typo, or the scalar's own name).
	// The scalar's underlying type must be a primitive the framework
	// knows how to validate; user-defined types in this slot would
	// silently break inheritance and produce invalid Go.
	CodeScalarBadPrimitive = "scalar/bad-primitive"
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
