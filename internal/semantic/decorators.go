package semantic

// Decorator registry - the single source of truth describing every
// decorator the semantic analyser, codegen, and LSP know about.
//
// The placement check (see [analyzer.checkDecoratorPlacement]) reads
// [Registry] to decide whether `@name` may appear at a given declaration
// site. The same data backs LSP completion, hover docs, and the README's
// compatibility table - so adding a decorator means adding one entry
// here, not editing several files.
//
// [Spec] carries placement, a one-line doc string, and the positional
// argument shape ([ArgsRule]); the argument-shape pass validates arity,
// value types, and enum sets against it.

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

// Level is a bitmask of declaration sites where a decorator may appear.
// A [Spec] OR-s the levels it accepts; the placement check passes when
// at least one bit overlaps with the current site. Single-bit values are
// used for diagnostic rendering - never combine bits when calling
// [Level.Name].
type Level uint32

const (
	// LvlFile is a file-header decorator, before `package`. Examples:
	// `@doc("...")`, `@deprecated`.
	LvlFile Level = 1 << iota
	// LvlType is a `type Name { ... }` declaration.
	LvlType
	// LvlField is a field inside a `type` body. Fields inside an
	// `error` body use [LvlErrorField] instead so request-only and
	// validator decorators are rejected on server-emitted payloads.
	LvlField
	// LvlService is a `service Name { ... }` (primary only - `extend
	// service` rejects service-level decorators upstream).
	LvlService
	// LvlMethod is a method inside a service body.
	LvlMethod
	// LvlEnum is an `enum Name { ... }` declaration.
	LvlEnum
	// LvlEnumValue is a single value inside an enum body.
	LvlEnumValue
	// LvlError is an `error Cat Name [{ ... }]` declaration.
	LvlError
	// LvlScalar is a `scalar Name Primitive` declaration.
	LvlScalar
	// LvlMiddleware is a `middleware Name(...)` declaration.
	LvlMiddleware
	// LvlEvent is an `event Name { ... }` inside a service body.
	LvlEvent
	// LvlConsumer is a `consume Name { ... }` inside a service body.
	LvlConsumer
	// LvlErrorField is a field inside an `error` body. Distinct from
	// [LvlField] because errors are server-emitted, so request-only
	// decorators (`@path`, `@query`, `@body`, `@form`, `@maxSize`,
	// `@mimeTypes`) are rejected. Schema validators (`@minLength`,
	// `@maxLength`, `@pattern`, `@gte`, ...) are accepted but
	// contribute only to OpenAPI schema constraints - codegen does
	// not generate a runtime `Validate()` for ErrorDecl types.
	LvlErrorField
)

// levelNames pairs each single-bit level with its human label, in stable
// order. The order matters: [Level.String] iterates this slice so the
// rendered list is deterministic across runs (important for golden tests
// and diff-friendly diagnostics).
var levelNames = []struct {
	bit  Level
	name string
}{
	{LvlFile, "file"},
	{LvlType, "type"},
	{LvlField, "field"},
	{LvlService, "service"},
	{LvlMethod, "method"},
	{LvlEnum, "enum"},
	{LvlEnumValue, "enum value"},
	{LvlError, "error"},
	{LvlScalar, "scalar"},
	{LvlMiddleware, "middleware"},
	{LvlEvent, "event"},
	{LvlConsumer, "consumer"},
	{LvlErrorField, "error field"},
}

// Name returns the label for a single-bit level. It returns "unknown"
// for the zero value or a multi-bit mask - callers rendering a multi-bit
// mask should use [Level.String] instead.
func (l Level) Name() string {
	for _, e := range levelNames {
		if l == e.bit {
			return e.name
		}
	}
	return "unknown"
}

// String renders every set bit of l joined by ", ", e.g.
// "field, scalar". Used to format the "@X is only allowed on {levels}"
// hint. Returns "(none)" for the zero mask so empty Specs surface as a
// configuration bug rather than a blank message.
func (l Level) String() string {
	if l == 0 {
		return "(none)"
	}
	var parts []string
	for _, e := range levelNames {
		if l&e.bit != 0 {
			parts = append(parts, e.name)
		}
	}
	return strings.Join(parts, ", ")
}

// ArgKind classifies the literal shape expected at a positional argument
// slot. The argument-validation pass maps an [ast.Expr] to one of these
// kinds and rejects mismatches with [CodeDecoratorArgType].
type ArgKind uint8

const (
	// ArgAny accepts any expression. Use sparingly - prefer a tighter
	// kind so the IDE can give a useful "expected X" hint.
	ArgAny ArgKind = iota
	// ArgString matches a [ast.StringLit] (regular or raw).
	ArgString
	// ArgInt matches a [ast.IntLit].
	ArgInt
	// ArgNumber matches int OR float.
	ArgNumber
	// ArgBool matches a [ast.BoolLit].
	ArgBool
	// ArgIdent matches a bare identifier ([ast.IdentExpr]).
	ArgIdent
	// ArgDuration matches a [ast.DurationLit] (`5s`, `100ms`, ...).
	ArgDuration
	// ArgSize matches a [ast.SizeLit] (`1MB`, `8KB`, ...).
	ArgSize
	// ArgStringOrIdent accepts either, used by `@tags` where humans
	// commonly write `@tags(users)` and `@tags("user-mgmt")`
	// interchangeably.
	ArgStringOrIdent
)

// String returns the human label used in `expected X, got Y` messages.
// Stable across versions - IDE error explainers reference these names.
func (k ArgKind) String() string {
	switch k {
	case ArgString:
		return "string"
	case ArgInt:
		return "int"
	case ArgNumber:
		return "int or float"
	case ArgBool:
		return "bool"
	case ArgIdent:
		return "identifier"
	case ArgDuration:
		return "duration"
	case ArgSize:
		return "size"
	case ArgStringOrIdent:
		return "string or identifier"
	default:
		return "any"
	}
}

// ArgsRule captures the positional argument shape of a decorator. Named
// arguments (`name: value`), nested decorators, and object literals are
// outside its scope.
type ArgsRule struct {
	// Min is the minimum number of positional arguments. 0 allows the
	// no-args form (`@deprecated`).
	Min int
	// Max is the maximum number of positional arguments; -1 means
	// unbounded (variadic).
	Max int
	// Kinds is the per-position expected kind. When the actual arg
	// count exceeds len(Kinds), [Variadic] applies to the remainder.
	Kinds []ArgKind
	// Variadic is the kind for arguments beyond len(Kinds). Only
	// meaningful when Max < 0 or Max > len(Kinds).
	Variadic ArgKind
	// Enum, when non-empty, restricts the first positional argument
	// value (string OR ident) to this set. Used by `@format` to
	// constrain string formats (`email`, `uuid`, ...).
	Enum []string
	// AllowArrayShortcut treats a single array-literal positional arg
	// as variadic-equivalent. Used by `@requiresOneOf(["a","b"])`,
	// `@mimeTypes(["a/b","c/d"])` etc., where humans naturally write
	// the list in brackets. The array's elements are validated against
	// [Variadic]; element count must still satisfy [Min]..[Max].
	AllowArrayShortcut bool
}

// Prims is a bitmask of primitive type categories a validator
// decorator can target. Used by the field-type compatibility check
// (`@length` only makes sense on strings, `@uniqueItems` only on
// arrays, etc.). A zero value means "no constraint" - applies to
// anything, used by metadata decorators like `@doc`.
type Prims uint8

const (
	// PrimString covers `string` and any scalar whose primitive is
	// string. Bytes/format/uri all reduce to this category.
	PrimString Prims = 1 << iota
	// PrimNumber covers signed/unsigned integers and floats.
	PrimNumber
	// PrimBool covers `bool`.
	PrimBool
	// PrimArray covers `T[]` and `map<K,V>` field shapes (arrays and
	// maps share validation: count, uniqueness).
	PrimArray
	// PrimFile covers the `file` primitive (multipart upload).
	PrimFile
	// PrimDateTime covers the `datetime` primitive, which no validator
	// targets: a timestamp has no length, bound or format to check.
	PrimDateTime
	// PrimAny matches any field type - used by validator-style
	// decorators that don't care about primitive (e.g. `@example`).
	PrimAny Prims = 0
)

// String renders a Prims set as a comma-joined list for diagnostics.
// Used in "@length is for string fields, this field is bool" hints.
func (p Prims) String() string {
	if p == 0 {
		return "any"
	}
	var parts []string
	if p&PrimString != 0 {
		parts = append(parts, "string")
	}
	if p&PrimNumber != 0 {
		parts = append(parts, "number")
	}
	if p&PrimBool != 0 {
		parts = append(parts, "bool")
	}
	if p&PrimArray != 0 {
		parts = append(parts, "array")
	}
	if p&PrimFile != 0 {
		parts = append(parts, "file")
	}
	if p&PrimDateTime != 0 {
		parts = append(parts, "datetime")
	}
	return strings.Join(parts, ", ")
}

// ConstraintFamily classifies what a constraint decorator restricts.
// Targets read it to decide which checks or schema keywords a decorator
// contributes, so the classification is stated once here rather than
// re-derived per target.
type ConstraintFamily uint8

const (
	// ConstraintNumeric bounds a number: @gt, @gte, @lt, @lte, @range,
	// @positive, @negative, @multipleOf.
	ConstraintNumeric ConstraintFamily = 1 << iota
	// ConstraintLength bounds text length: @length, @minLength, @maxLength.
	ConstraintLength
	// ConstraintText restricts text shape: @pattern, @format.
	ConstraintText
	// ConstraintItems bounds a collection: @minItems, @maxItems,
	// @uniqueItems.
	ConstraintItems
	// ConstraintRuntime is checked at runtime but has no schema form:
	// @maxSize, @mimeTypes on a multipart part.
	ConstraintRuntime
)

// ConstraintNarrowing is every family a field may stack on a referenced
// type; a $ref field is never a collection or a file.
const ConstraintNarrowing = ConstraintNumeric | ConstraintLength | ConstraintText

// ConstraintSchema is every family with a schema form.
const ConstraintSchema = ConstraintNumeric | ConstraintLength | ConstraintText | ConstraintItems

// Spec describes one decorator: its canonical name, every site it may
// appear, a short doc string for IDE hover, and the positional argument
// shape. Every decorator validates through the generic
// [analyzer.checkPositionalArgs] path - there are no per-decorator
// argument shape hooks.
type Spec struct {
	// Name is the bare decorator name (no leading `@`). Stored so callers
	// holding a *Spec can render diagnostics without a separate lookup.
	Name string
	// Levels is the OR of every site where `@Name` is legal. The
	// placement check fails when the current site bit is not set.
	Levels Level
	// Doc is a one-line description shown in LSP hover. Keep it short -
	// the README is the long-form reference.
	Doc string
	// Args is the positional argument shape; the zero value means
	// "no args expected".
	Args ArgsRule
	// AppliesTo restricts the decorator to fields / scalars whose
	// primitive type is in the listed categories. Zero (PrimAny)
	// means no constraint - used by metadata-style decorators. The
	// field-type compatibility check reads this when LvlField or
	// LvlScalar is the current site.
	AppliesTo Prims
	// Constraint classifies the decorator as a constraint and says what
	// it restricts. Zero means the decorator is not a constraint
	// (metadata, binding, routing).
	Constraint ConstraintFamily
	// Flag reports whether the decorator never accepts arguments. It
	// is a presentation hint, not a parser rule:
	//
	//   - LSP completion inserts `@positive` (no parens) for Flag
	//     decorators and `@range($1, $2)` (snippet placeholders) for
	//     the rest.
	//   - `craftgo fmt` strips empty parens (`@positive()` →
	//     `@positive`) so canonical form is parens-free.
	//   - The parser emits [CodeFlagEmptyParens] (warning) when a Flag
	//     decorator is written with empty `()`. Warning only - the
	//     formatter rewrites it on save.
	//
	// The invariant is `Flag == true ⇒ Args.Max == 0`.
	Flag bool
	// Repeatable reports whether multiple `@Name` occurrences on one site
	// are the intended idiom (each adds to an aggregate: tags merge,
	// middlewares chain, security OR-alternatives, errors accumulate) rather
	// than a duplicate. The duplicate-decorator check reads this so the rule
	// lives ONCE here instead of a separate hardcoded list that drifts.
	Repeatable bool
	// Metadata marks a decorator that only documents its target (`@doc`,
	// `@deprecated`, `@example`) and never shapes the wire or the
	// generated code. Every other field decorator contradicts
	// `@sensitive`, whose field never crosses the wire.
	Metadata bool
}

// formatValues lists the named string formats accepted by `@format` on a
// field or scalar: the [strfmt] catalogue codegen emits the checks from,
// so the legal-name set and the validator set cannot drift.
var formatValues = strfmt.Names()

// Registry is the closed set of decorators the framework recognises. A
// `@name` not present here is reported as `decorator/unknown` - there is
// no escape-hatch by design (see README §"Triết lý").
//
// Levels mirror the table in README §"Decorator compatibility matrix";
// keep the two in sync. When in doubt the README table wins, because
// users read it first.
var Registry = map[string]Spec{
	// ---- Universal documentation / lifecycle ----
	"doc": {
		Name:     "doc",
		Levels:   LvlFile | LvlType | LvlField | LvlService | LvlMethod | LvlEnum | LvlEnumValue | LvlError | LvlScalar | LvlMiddleware | LvlEvent | LvlConsumer | LvlErrorField,
		Doc:      "Free-form documentation surfaced in OpenAPI / AsyncAPI and IDE hover.",
		Args:     ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
		Metadata: true,
	},
	"deprecated": {
		Name:     "deprecated",
		Levels:   LvlFile | LvlType | LvlField | LvlService | LvlMethod | LvlEnumValue | LvlMiddleware | LvlEvent | LvlConsumer | LvlErrorField,
		Doc:      "Marks the construct as deprecated; OpenAPI / AsyncAPI emit the deprecated flag.",
		Args:     ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}},
		Metadata: true,
	},
	"example": {
		Name:     "example",
		Levels:   LvlField | LvlErrorField,
		Doc:      "Example value rendered in the OpenAPI schema for this field. Argument is a literal (string / int / float / bool / null) or an array of those. Object examples are not accepted - a struct example is composed from each field's own @example; document a free-form any/map field's shape with @doc.",
		Args:     ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgAny}},
		Metadata: true,
	},
	// ---- OpenAPI file-header metadata ----
	// Per ast.File comment, file-level decorators carry top-of-file
	// OpenAPI metadata when no design-yaml override is supplied. Not in
	// the README §"Decorator compatibility matrix" table - kept here as
	// the runtime / fixtures rely on them.
	"version": {
		Name:   "version",
		Levels: LvlFile,
		Doc:    "OpenAPI document version (overrides craftgo.design.yaml openapi.version).",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},

	// ---- Type-level structural ----
	"requiresOneOf": {
		Name:   "requiresOneOf",
		Levels: LvlType,
		Doc:    "At least one of the listed fields must be present. Args: variadic idents/strings or a single array literal.",
		Args: ArgsRule{
			Min: 1, Max: -1, Variadic: ArgStringOrIdent,
			AllowArrayShortcut: true,
		},
	},
	"mutuallyExclusive": {
		Name:   "mutuallyExclusive",
		Levels: LvlType,
		Doc:    "At most one of the listed fields may be present. Args: variadic idents/strings or a single array literal.",
		Args: ArgsRule{
			Min: 1, Max: -1, Variadic: ArgStringOrIdent,
			AllowArrayShortcut: true,
		},
	},

	// ---- Field validation: string ----
	// On request bodies (LvlField) these emit runtime validators; on
	// error bodies (LvlErrorField) errors are server-emitted so they
	// surface only as OpenAPI schema constraints - no runtime check
	// is generated for ErrorDecl types.
	"length": {
		Name: "length", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Exact or [min,max] length for strings.",
		Args:       ArgsRule{Min: 1, Max: 2, Kinds: []ArgKind{ArgInt, ArgInt}},
		AppliesTo:  PrimString,
		Constraint: ConstraintLength,
	},
	"minLength": {
		Name: "minLength", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Minimum string length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimString,
		Constraint: ConstraintLength,
	},
	"maxLength": {
		Name: "maxLength", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Maximum string length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimString,
		Constraint: ConstraintLength,
	},
	"pattern": {
		Name: "pattern", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "RE2 regex the value must match.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
		AppliesTo:  PrimString,
		Constraint: ConstraintText,
	},
	"format": {
		Name:   "format",
		Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:    "Named format constraint (e.g. email, uuid, datetime).",
		Args: ArgsRule{
			Min: 1, Max: 1,
			Kinds: []ArgKind{ArgStringOrIdent},
			Enum:  formatValues,
		},
		AppliesTo:  PrimString,
		Constraint: ConstraintText,
	},

	// ---- Field validation: number ----
	"gt": {
		Name: "gt", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Value must be strictly greater than N (x > N).",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo:  PrimNumber,
		Constraint: ConstraintNumeric,
	},
	"gte": {
		Name: "gte", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Value must be greater than or equal to N (x >= N).",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo:  PrimNumber,
		Constraint: ConstraintNumeric,
	},
	"lt": {
		Name: "lt", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Value must be strictly less than N (x < N).",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo:  PrimNumber,
		Constraint: ConstraintNumeric,
	},
	"lte": {
		Name: "lte", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Value must be less than or equal to N (x <= N).",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo:  PrimNumber,
		Constraint: ConstraintNumeric,
	},
	"range": {
		Name: "range", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Numeric range [min, max] - both bounds inclusive.",
		Args:       ArgsRule{Min: 2, Max: 2, Kinds: []ArgKind{ArgNumber, ArgNumber}},
		AppliesTo:  PrimNumber,
		Constraint: ConstraintNumeric,
	},
	"positive": {Name: "positive", Levels: LvlField | LvlScalar | LvlErrorField, Doc: "Value must be > 0.", AppliesTo: PrimNumber, Flag: true, Constraint: ConstraintNumeric},
	"negative": {Name: "negative", Levels: LvlField | LvlScalar | LvlErrorField, Doc: "Value must be < 0.", AppliesTo: PrimNumber, Flag: true, Constraint: ConstraintNumeric},
	"multipleOf": {
		Name: "multipleOf", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Value must be a multiple of N.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo:  PrimNumber,
		Constraint: ConstraintNumeric,
	},

	// ---- Field validation: array / map ----
	"minItems": {
		Name: "minItems", Levels: LvlField | LvlErrorField,
		Doc:        "Minimum array / map length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimArray,
		Constraint: ConstraintItems,
	},
	"maxItems": {
		Name: "maxItems", Levels: LvlField | LvlErrorField,
		Doc:        "Maximum array / map length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimArray,
		Constraint: ConstraintItems,
	},
	"uniqueItems": {Name: "uniqueItems", Levels: LvlField | LvlErrorField, Doc: "Array elements must be unique.", AppliesTo: PrimArray, Flag: true, Constraint: ConstraintItems},

	// ---- Field validation: file ----
	"maxSize": {
		Name: "maxSize", Levels: LvlField,
		Doc:        "Upload size cap (bytes / KB / MB / GB).",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgSize}},
		AppliesTo:  PrimFile,
		Constraint: ConstraintRuntime,
	},
	"mimeTypes": {
		Name: "mimeTypes", Levels: LvlField,
		Doc:        "Allowed Content-Type list for uploads. Args: variadic strings or a single array literal.",
		Args:       ArgsRule{Min: 1, Max: -1, Variadic: ArgString, AllowArrayShortcut: true},
		AppliesTo:  PrimFile,
		Constraint: ConstraintRuntime,
	},

	// ---- Field metadata ----
	"default": {
		Name: "default", Levels: LvlField | LvlErrorField,
		Doc:  "Default value applied when field absent.",
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgAny}},
	},
	"nullable": {Name: "nullable", Levels: LvlField | LvlErrorField, Doc: "Marks the field as accepting an explicit JSON null.", Flag: true},
	"json": {
		Name: "json", Levels: LvlField | LvlErrorField,
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
		Doc:  "Sets the JSON key of a body field when it is not the field name - a contract another system owns, or a key the parser reads as a mixin (`OrderItem Item[]`). The Go struct tag, the documents and validation messages all use it. Not for a field bound off the body (@path / @query / @header / @cookie / @form), which names its own wire location.",
	},
	"sensitive": {
		Name: "sensitive", Levels: LvlField | LvlErrorField, Flag: true,
		Doc: "Server-only field: tagged `json:\"-\"` so neither the request decoder nor the response encoder touches it, and skipped entirely from OpenAPI. Cannot combine with any wire-shaping decorator: validators (@length / @gt / @gte / @lt / @lte / @range / @pattern / @format / @minItems / @maxItems / @multipleOf / @positive / @negative / @uniqueItems / @requiresOneOf / @mutuallyExclusive), nullability / defaults (@nullable / @default), or any binding (@body / @path / @query / @header / @cookie / @form). The field stays as a Go struct member that server logic populates / reads internally.",
	},

	// ---- Field binding ----
	"path":   {Name: "path", Levels: LvlField, Doc: "Bind from URL path parameter.", Args: ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}}},
	"query":  {Name: "query", Levels: LvlField, Doc: "Bind from URL query string.", Args: ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}}},
	"header": {Name: "header", Levels: LvlField | LvlErrorField, Doc: "Bind from HTTP request header (request fields) or write to response header (error fields).", Args: ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}}},
	"cookie": {Name: "cookie", Levels: LvlField | LvlErrorField, Doc: "Bind from HTTP cookie (request fields) or set a response cookie (error fields).", Args: ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}}},
	"body":   {Name: "body", Levels: LvlField, Doc: "Bind from request body.", Args: ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}}},
	"form":   {Name: "form", Levels: LvlField, Doc: "Bind from multipart form field.", Args: ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}}},

	// ---- Service ----
	"prefix": {
		Name: "prefix", Levels: LvlService,
		Doc:  "Path prefix prepended to every method route.",
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	// ---- Events ----
	"contract": {
		Name:   DecoratorContract,
		Levels: LvlEvent,
		Doc:    "Overrides the event's wire identity. Defaults to `<package>.<Event>`; set it to interoperate with a contract another system already publishes.",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"consumerGroup": {
		Name:   DecoratorConsumerGroup,
		Levels: LvlService | LvlConsumer,
		Doc:    "The broker identity a consumer joins - the Kafka consumer group, the NATS queue group. Consumers sharing a group divide the stream between them, which makes a group a unit of scaling and of failure isolation (not of ordering - no transport orders across contracts). On a transport that remembers a position per group, Kafka and JetStream, the name is also where those consumers resume: the default `<package>-<Service>-<Consumer>` moves when you rename any of the three, and a name the broker has never seen has no position at all, so if it has an offset, write the name down. On a service the decorator is the default for every `consume` in the body; on a consumer it overrides that. Consumers of different contracts may share one group inside a service; two services may not share one.",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},

	"group": {
		Name: "group", Levels: LvlService,
		Doc:  "Nests the service's generated handlers and service stubs under <service>/<group>/ on disk and adds its value as an OpenAPI tag on every method; does not affect the route or OpenAPI path. Accepts a nested path like \"admin/ops\".",
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"middlewares": {
		Name:       "middlewares",
		Levels:     LvlService | LvlMethod,
		Doc:        "Apply named middlewares; method-level appends to service-level chain. Args: variadic idents or a single array literal.",
		Args:       ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true},
		Repeatable: true,
	},
	"consumeMiddlewares": {
		Name:   "consumeMiddlewares",
		Levels: LvlService | LvlConsumer,
		Doc: "Apply named consume middlewares to a service's consumers; consumer-level appends to the service-level chain. " +
			"Names come from `consume middleware Name` declarations, never from `middleware Name` - the two wrap different things. " +
			"The first name is OUTERMOST: it sees the message first on the way in and returns last, so it is the one that decides " +
			"what the transport is told. A middleware that swallows an error goes first and one that retries goes last.",
		Args:       ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true},
		Repeatable: true,
	},
	"tags": {
		Name:       "tags",
		Levels:     LvlService | LvlMethod,
		Doc:        "OpenAPI tags. Method-level appends to service-level. Args: variadic idents/strings or a single array literal.",
		Args:       ArgsRule{Min: 1, Max: -1, Variadic: ArgStringOrIdent, AllowArrayShortcut: true},
		Repeatable: true,
	},
	"security": {
		Name:       "security",
		Levels:     LvlService | LvlMethod,
		Doc:        "Security scheme requirements (OpenAPI metadata). Args: variadic scheme idents or a single array literal. Within one decorator the schemes AND-combine; multiple `@security(...)` decorators OR-combine.",
		Args:       ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true},
		Repeatable: true,
	},
	"ignoreMiddleware": {
		Name:   "ignoreMiddleware",
		Levels: LvlMethod | LvlConsumer,
		Doc:    "Clear the inherited middleware chain on this member. The site picks the chain: on a method the inherited @middlewares, on a consumer the inherited @consumeMiddlewares. The member's own decorator then starts from empty instead of appending to the service-level chain.",
		Args:   ArgsRule{Min: 0, Max: 0},
	},
	"ignoreSecurity": {
		Name:   "ignoreSecurity",
		Levels: LvlMethod,
		Doc:    "Clear the inherited @security chain on this method. Useful for public endpoints inside an otherwise-authenticated service.",
		Args:   ArgsRule{Min: 0, Max: 0},
	},
	"ignoreTags": {
		Name:   "ignoreTags",
		Levels: LvlMethod,
		Doc:    "Clear the inherited @tags list on this method. Method-level @tags(...) then start from empty.",
		Args:   ArgsRule{Min: 0, Max: 0},
	},

	// ---- Method-only ----
	"summary":     {Name: "summary", Levels: LvlMethod, Doc: "One-line OpenAPI operation summary.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}}},
	"operationId": {Name: "operationId", Levels: LvlMethod, Doc: "Override OpenAPI operationId.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}}},
	"errors":      {Name: "errors", Levels: LvlMethod, Doc: "Declared error responses for OpenAPI. Args: variadic error idents or a single array literal.", Args: ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true}, Repeatable: true},
	"status":      {Name: "status", Levels: LvlMethod, Doc: "Override default success status code.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}}},
	// `@consumes`, `@produces`, `@accepts` are not in the registry.
	// craftgo's transport hardcodes `application/json` for both request
	// decode and response encode, so accepting those decorators would
	// parse but produce no runtime / spec effect. The registry keeps
	// only decorators with a real effect.

	// ---- Method behavior ----
	// Raw modes hand one or both transport sides to logic. A `request` /
	// `response` block on a raw side is a docs-only contract: it shapes
	// the OpenAPI document and the generated Go types, but the transport
	// neither binds / validates (raw request) nor encodes (raw response)
	// it. `@passthrough` is exactly `@rawRequest @rawResponse`; every
	// layer reads the modes through wire.RawSides.
	"passthrough": {Name: "passthrough", Levels: LvlMethod, Doc: "Hand both transport sides to logic: the entry point receives the raw http.ResponseWriter and *http.Request and writes the response directly. Equivalent to @rawRequest @rawResponse. Optional request/response blocks are a docs-only contract (OpenAPI + generated types).", Flag: true},
	"rawRequest":  {Name: "rawRequest", Levels: LvlMethod, Doc: "Hand the request side to logic: the entry point receives the raw *http.Request and the framework skips request bind + validate. A request block is a docs-only contract (OpenAPI + generated type). The response stays framework-encoded unless @rawResponse is also set.", Flag: true},
	"rawResponse": {Name: "rawResponse", Levels: LvlMethod, Doc: "Hand the response side to logic: the entry point receives the http.ResponseWriter and writes status, headers and body itself; the framework skips the response encode. A response block is a docs-only contract (OpenAPI + generated type). The request stays bound + validated unless @rawRequest is also set.", Flag: true},

	// ---- Method limits ----
	// `@timeout` derives a context deadline covering the full handler
	// lifecycle (decode body → user logic → encode response). Handlers
	// that honour ctx.Done() return early; nothing is cut off on the
	// wire, so it applies to every mode, raw sides included. Independent
	// from transport-level deadlines (`http.Server.ReadTimeout` /
	// `WriteTimeout`) which the user configures on the server itself
	// when the stdlib defaults are not enough.
	"timeout":     {Name: "timeout", Levels: LvlMethod, Doc: "Cap the handler's execution time: the request context is cancelled when the deadline elapses (no status is written automatically). Overrides the global handlerTimeout for this route.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgDuration}}},
	"maxBodySize": {Name: "maxBodySize", Levels: LvlMethod, Doc: "Cap the request body size in bytes. Two enforcement points fire: Content-Length pre-check returns 413 immediately when the declared size exceeds the cap, and MaxBytesReader wraps r.Body so reads past the cap surface as a 400 Read error. Multipart parsers also lift their in-memory budget to this value.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgSize}}},
}

// Lookup returns the [Spec] for `name` and whether it is registered.
// Convenience wrapper kept exported so the LSP / CLI can introspect the
// registry without poking the bare map.
func Lookup(name string) (Spec, bool) {
	s, ok := Registry[name]
	return s, ok
}

// removed maps a decorator craftgo once accepted to the sentence telling
// an author what takes its place. A design written against an older
// craftgo then gets a migration note instead of "not in the framework
// registry", which says nothing about what to do.
//
// It is deliberately separate from [Registry]: a removed decorator is not
// a decorator, so nothing that walks the registry - completion, hover,
// the argument-shape pass - has to learn to skip it.
var removed = map[string]string{
	"key": "@key was removed: which entity a message belongs to is decided when it is published, not by the contract. " +
		"Pass the key to the generated publisher instead - `PublishOrderPlaced(ctx, payload, craftevents.WithKey(string(payload.OrderID)))`, " +
		"or `craftevents.WithKey(...)` on a batch entry.",
}

// RemovedDecorator returns the migration note for a decorator craftgo has
// removed, and whether `name` is one. The LSP reads it so hovering the
// decorator in an unmigrated design says the same thing the diagnostic
// does.
func RemovedDecorator(name string) (string, bool) {
	msg, ok := removed[name]
	return msg, ok
}

// ConstraintOf returns the decorator's constraint classification, or zero
// when the name is unknown or the decorator is not a constraint.
func ConstraintOf(name string) ConstraintFamily {
	spec, ok := Lookup(name)
	if !ok {
		return 0
	}
	return spec.Constraint
}
