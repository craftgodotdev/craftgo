package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

const (
	// CodeDecoratorUnknown fires when `@name` is not in the decorator registry.
	CodeDecoratorUnknown = "decorator/unknown"
	// CodeDecoratorRemoved fires when `@name` is a removed decorator.
	CodeDecoratorRemoved = "decorator/removed"
	// CodeDecoratorPlacement fires when a decorator sits outside its [Spec.Levels].
	CodeDecoratorPlacement = "decorator/placement"
	// CodeDecoratorDuplicate fires when one scope repeats a non-repeatable decorator.
	CodeDecoratorDuplicate = "decorator/duplicate"
	// CodeDecoratorArity fires when a decorator has fewer or more arguments than its spec allows.
	CodeDecoratorArity = "decorator/arity"
	// CodeDecoratorArgType fires when an argument's literal kind does not fit its position.
	CodeDecoratorArgType = "decorator/argtype"
	// CodeDecoratorArgValue fires when an argument is outside the allowed value set (`@format(garbage)`).
	CodeDecoratorArgValue = "decorator/argvalue"
	// CodeDecoratorRange fires when a numeric pair is reversed (`@length(20, 5)`) or a value breaks a bound.
	CodeDecoratorRange = "decorator/range"
	// CodeDecoratorTypeMismatch fires when a validator decorates the wrong primitive (`@length` on `int`).
	CodeDecoratorTypeMismatch = "decorator/typemismatch"
	// CodeDecoratorRef fires when a decorator argument names no declared error, middleware, field or scheme.
	CodeDecoratorRef = "decorator/ref"
	// CodeDecoratorRedundant warns when a decorator repeats what its site says (`@nullable` on `T?`).
	CodeDecoratorRedundant = "decorator/redundant"
	// CodeDecoratorConflict fires when a decorator contradicts another decorator or its site.
	CodeDecoratorConflict = "decorator/conflict"
	// CodeDefaultNeedsOptional warns when `@default` sits on a field that is neither optional nor `@path`.
	CodeDefaultNeedsOptional = "decorator/default-needs-optional"
	// CodeFlagEmptyParens warns when a flag decorator is written with empty parens (`@positive()`).
	CodeFlagEmptyParens = "decorator/flag-empty-parens"
	// CodeArgPreferIdent warns when a registered identifier argument is quoted (`@format("email")`).
	CodeArgPreferIdent = "decorator/arg-prefer-ident"
	// CodeBoundOverflow fires when a bound or default overflows the field type (`int8 @lte(300)`).
	CodeBoundOverflow = "decorator/bound-overflow"
	// CodeBoundEmptyRange fires when two bounds meet at a value a strict one excludes
	// (`@gt(5) @lt(5)`, `@positive @negative`).
	CodeBoundEmptyRange = "decorator/empty-range"
	// CodeMutExSingleField warns when `@mutuallyExclusive` lists fewer than two distinct fields.
	CodeMutExSingleField = "decorator/single-field-mutex"
	// CodeDuplicateGroupField warns when a cross-field decorator lists a field twice.
	CodeDuplicateGroupField = "decorator/duplicate-group-field"
	// CodeCrossFieldNotOptional fires when a cross-field decorator names a field unfit for the group.
	CodeCrossFieldNotOptional = "decorator/cross-field-not-optional"
	// CodeMapKeyType fires when a map key is not a string or integer, or a scalar or enum over one.
	CodeMapKeyType = "type/map-key"
	// CodeMapValueType fires when a map value is an optional array or map (`map<string, int[]?>`).
	CodeMapValueType = "type/map-value"
	// CodeDuplicatePathVar fires when a route repeats a path variable (`/items/{id}/x/{id}`).
	CodeDuplicatePathVar = "route/duplicate-path-var"
	// CodeRoutePattern fires when a route holds a segment net/http's ServeMux refuses to
	// register (`/org-{org}`, a `{rest...}` before the last segment).
	CodeRoutePattern = "route/pattern"
	// CodeDuplicateWireName fires when two request fields bind the same wire name in one location.
	CodeDuplicateWireName = "binding/duplicate-wire-name"

	// CodeDuplicateDecl fires when two top-level declarations of one namespace share a name.
	CodeDuplicateDecl = "decl/duplicate"
	// CodeDeclBuiltinName fires when a type, enum, scalar or error is named after a built-in type.
	CodeDeclBuiltinName = "decl/builtin-name"
	// CodeDeclNameCase fires when a declaration or method name would make a generated Go
	// identifier unexported; a lower-case service name warns.
	CodeDeclNameCase = "decl/name-case"
	// CodeFieldNameCollision fires when two fields of one body share a Go name (warning) or a JSON key.
	CodeFieldNameCollision = "field/name-collision"
	// CodeEnumValueCollision warns when two values of one enum share a Go constant name.
	CodeEnumValueCollision = "enum/value-collision"
	// CodeDeclGoNameCollision fires when two declarations produce one Go name (`type FooErr`, `error Foo`).
	CodeDeclGoNameCollision = "decl/go-name-collision"

	// CodeDuplicateField fires when two fields of one type or error body share a name.
	CodeDuplicateField = "field/duplicate"

	// CodeInvalidGoName fires when a field name maps to an empty or digit-leading Go name (`_`, `_2`),
	// or a field or mixin to a Go name a generated method or embedded struct takes (`validate`).
	CodeInvalidGoName = "field/invalid-go-name"

	// CodeEnumDuplicateName fires when two values of one enum share a name.
	CodeEnumDuplicateName = "enum/duplicate-name"
	// CodeEnumMixedTypes fires when an enum mixes bare, int and string values.
	CodeEnumMixedTypes = "enum/mixed-types"
	// CodeEnumDuplicateLiteral fires when two values of one enum share a literal.
	CodeEnumDuplicateLiteral = "enum/duplicate-literal"
	// CodeEnumEmpty fires when an enum declares no values.
	CodeEnumEmpty = "enum/empty-values"

	// CodeServiceDuplicate fires when two primary `service` declarations share a name.
	CodeServiceDuplicate = "service/duplicate"
	// CodeServiceExtendOrphan fires when an `extend service` has no primary declaration in its package.
	CodeServiceExtendOrphan = "service/extend-orphan"
	// CodeExtendDecoratorNotMethod fires when an `extend service` carries a non-method decorator (`@prefix`) or `@operationId`.
	CodeExtendDecoratorNotMethod = "service/extend-decorator-not-method"
	// CodeServiceDuplicateMethod fires when one service declares two methods of the same name.
	CodeServiceDuplicateMethod = "service/duplicate-method"
	// CodeMethodNameClash fires when methods scaffolded into one output directory write one file
	// (`GetURL` beside `GetUrl`) or declare one Go name (`X` beside `NewX`), or a method is named
	// `Logger`.
	CodeMethodNameClash = "service/method-name-clash"
	// CodeMethodFileName fires when a method's file is one the go command ignores or builds only for
	// tests or one system: `RunTest` writes `run_test.go`, `ListWindows` `list_windows.go`.
	CodeMethodFileName = "service/method-file-name"
	// CodeServiceDuplicateRoute fires when one service declares two methods of one verb and route shape.
	CodeServiceDuplicateRoute = "service/duplicate-route"

	// CodeBindingConflict fires when a field carries more than one binding decorator.
	CodeBindingConflict = "binding/conflict"
	// CodeBindingType fires when a field or method type cannot ride the binding or body it is given.
	CodeBindingType = "binding/type"
	// CodeBindingVerb fires when `@body` or `@form` sits on a request field of a body-less verb.
	CodeBindingVerb = "binding/verb"
	// CodeBindingFormWithoutFile fires when `@form` sits on a field of a request that carries no `file`.
	CodeBindingFormWithoutFile = "binding/form-without-file"
	// CodeFilePosition fires when a `file` sits below a request's top level, or in a response, an
	// error body or an event payload.
	CodeFilePosition = "binding/file-position"
	// CodeGroupPackageStraddle fires when services of different DSL packages share an output directory.
	CodeGroupPackageStraddle = "group/package-straddle"
	// CodeGroupMethodCollision fires when services sharing an output directory declare one method name.
	CodeGroupMethodCollision = "group/method-collision"
	// CodeMiddlewareCollision fires when two packages declare a middleware of the same name, or
	// two middlewares write one scaffold file (`APIKey` beside `ApiKey`).
	CodeMiddlewareCollision = "middleware/collision"

	// CodeQualifiedRef fires when a reference has two qualifiers or qualifies its own package.
	CodeQualifiedRef = "ref/qualified"

	// CodeMixinNonType fires when a mixin names an enum, error, scalar or middleware.
	CodeMixinNonType = "mixin/non-type"
	// CodeMixinCycle fires when a mixin embeds a type already on its expansion stack.
	CodeMixinCycle = "mixin/cycle"
	// CodeMixinConflict fires when embedding repeats a field or Go name, or embeds a type parameter.
	CodeMixinConflict = "mixin/conflict"
	// CodeMixinArity fires when a generic mixin has the wrong number of arguments.
	CodeMixinArity = "mixin/arity"

	// CodeGenericArity fires when a generic reference has the wrong number of arguments.
	CodeGenericArity = "generic/arity"
	// CodeGenericNonGeneric fires when a non-generic type or a type parameter is given `<...>` arguments.
	CodeGenericNonGeneric = "generic/non-generic"
	// CodeGenericOptionalArg fires when a generic type argument is optional (`Page<Item?>`).
	CodeGenericOptionalArg = "generic/optional-arg"
	// CodeGenericInstantiationCycle fires when a generic type instantiates itself, directly or
	// through other generics, with an argument built from its own type parameter (`Tree<Tree<T>>`).
	CodeGenericInstantiationCycle = "generic/instantiation-cycle"

	// CodePathBaseFormat warns when [Options.BasePath] lacks a leading `/`, ends with `/` or contains `//`.
	CodePathBaseFormat = "path/base-format"
	// CodePathCollision fires when two methods' routes cannot both register with net/http's ServeMux.
	CodePathCollision = "path/collision"
	// CodeDuplicateOperation fires when two methods resolve to the same OpenAPI operationId.
	CodeDuplicateOperation = "operation/duplicate-id"
	// CodePathParamMissing fires when a route's `{name}` has no request field to bind it.
	CodePathParamMissing = "path/param-missing"
	// CodePathParamOrphan fires when an `@path` field names no `{name}` of its route.
	CodePathParamOrphan = "path/param-orphan"
	// CodePathHealthConflict fires when a method's route equals a reserved health path.
	CodePathHealthConflict = "path/health-conflict"

	// CodePackageMissing fires when a file declares something without a `package` clause.
	CodePackageMissing = "package/missing"
	// CodePackageName fires when a package name is one no generated Go package can take: a Go
	// keyword or predeclared identifier, `main`, `init` or `_`.
	CodePackageName = "package/name"
	// CodeImportUnresolved fires when `import "path"` names no design folder.
	CodeImportUnresolved = "import/unresolved"
	// CodeImportEscape fires when an import path is absolute or its first segment is `.` or `..`.
	CodeImportEscape = "import/escape"
	// CodeImportDuplicate fires when one file imports a path twice.
	CodeImportDuplicate = "import/duplicate"
	// CodeImportAliasConflict fires when two imports of one file share an alias, explicit or implicit.
	CodeImportAliasConflict = "import/alias-conflict"
	// CodeImportSelf warns when a file imports a folder named after its own package.
	CodeImportSelf = "import/self"
	// CodeRefUnknownPackage fires when a qualified reference names an undeclared package.
	CodeRefUnknownPackage = "ref/unknown-package"
	// CodeRefUnknownSymbol fires when a type reference names nothing usable as a type.
	CodeRefUnknownSymbol = "ref/unknown-symbol"
	// CodeRefPackageCycle fires when the type and error declarations of packages reference each other in a cycle.
	CodeRefPackageCycle = "ref/package-cycle"
	// CodeScalarBadPrimitive fires when a scalar wraps a non-built-in, `any`, `datetime` or `file`.
	CodeScalarBadPrimitive = "scalar/bad-primitive"
	// CodeEventPayloadMissing fires when an event has no `payload` clause.
	CodeEventPayloadMissing = "event/payload-missing"
	// CodeEventPayloadKind fires when an event's payload names a built-in, an enum or a scalar.
	CodeEventPayloadKind = "event/payload-kind"
	// CodeEventPayloadBinding fires when an event's payload reaches a field bound to `@path`, `@query`,
	// `@header`, `@cookie` or `@form`.
	CodeEventPayloadBinding = "event/payload-binding"
	// CodeEventContractCollision fires when two events resolve to one contract name.
	CodeEventContractCollision = "event/contract-collision"
	// CodeEventContractFormat fires when an `@contract` argument is empty or contains whitespace.
	CodeEventContractFormat = "event/contract-format"
	// CodeEventDuplicate fires when one package declares two events of the same name.
	CodeEventDuplicate = "event/duplicate-name"
)

// related returns a one-entry [lexer.Related] list.
func related(pos lexer.Position, msg string) []lexer.Related {
	return []lexer.Related{{Pos: pos, Msg: msg}}
}

// decoratorEnd returns the position just past d's `@name` token.
func decoratorEnd(d *ast.Decorator) lexer.Position {
	end := d.Pos
	w := 1 + len(d.Name)
	end.Column += w
	end.Offset += w
	return end
}
