package semantic

// Decorator registry - the single source of truth describing every
// decorator the semantic analyser, codegen, and LSP know about.
//
// The placement check (see [analyzer.checkDecoratorPlacement]) reads
// [Registry] to decide whether `@name` may appear at a given declaration
// site. The same data is intended to back LSP completion, hover docs, and
// the README's compatibility table - so adding a decorator means adding
// one entry here, not editing several files.
//
// Argument-shape validation (arity, value types, enum sets) lives in a
// follow-up pass; today [Spec] only carries placement and a one-line doc
// string. The struct is laid out with future fields in mind so the JSON
// schema for the LSP can be derived without further churn.

import "strings"

// Level is a bitmask of declaration sites where a decorator may appear.
// A [Spec] OR-s the levels it accepts; the placement check passes when
// at least one bit overlaps with the current site. Single-bit values are
// used for diagnostic rendering - never combine bits when calling
// [Level.Name].
type Level uint16

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
	// LvlErrorField is a field inside an `error` body. Distinct from
	// [LvlField] because errors are server-emitted, so request-only
	// decorators (`@path`, `@query`, `@body`, `@form`, `@maxSize`,
	// `@mimeTypes`) are rejected. Schema validators (`@minLength`,
	// `@pattern`, `@min`, ...) are still accepted but contribute only
	// to OpenAPI schema constraints - codegen does not generate a
	// runtime `Validate()` for ErrorDecl types.
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
// validated by per-decorator hooks in [analyzer.checkArgsCustom] and do
// not appear here.
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
	return strings.Join(parts, ", ")
}

// Spec describes one decorator: its canonical name, every site it may
// appear, a short doc string for IDE hover, and the positional argument
// shape. Decorators with non-uniform argument shapes (e.g. `@security`'s
// optional `scopes:` named arg, `@externalDocs`'s string-or-object) are
// validated by per-decorator hooks; their [Args] entry covers the
// uniform part and the hook handles the rest.
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
	// Flag reports whether the decorator never accepts arguments. It
	// is a presentation hint, not a parser rule:
	//
	//   - LSP completion inserts `@positive` (no parens) for Flag
	//     decorators and `@range($1, $2)` (snippet placeholders) for
	//     the rest.
	//   - `craftgo fmt` strips empty parens (`@positive()` →
	//     `@positive`) so canonical form is parens-free.
	//   - The parser emits [CodeFlagEmptyParens] (warning) when a Flag
	//     decorator is written with empty `()`. Soft-migration: not an
	//     error, but the formatter rewrites it on save.
	//
	// The invariant is `Flag == true ⇒ Args.Max == 0`. A future check
	// in init() enforces this.
	Flag bool
}

// formatValues lists the named string formats accepted by `@format` on
// a field or scalar. Source: README §"Decorators by level".
var formatValues = []string{
	"email", "url", "uri", "uuid", "datetime", "date", "time",
	"phone", "hostname", "ipv4", "ipv6", "cidr", "mac",
	"creditcard", "base64", "base64url", "hexcolor", "json",
}

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
		Name:   "doc",
		Levels: LvlFile | LvlType | LvlField | LvlService | LvlMethod | LvlEnum | LvlEnumValue | LvlError | LvlScalar | LvlMiddleware | LvlErrorField,
		Doc:    "Free-form documentation surfaced in OpenAPI and IDE hover.",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"deprecated": {
		Name:   "deprecated",
		Levels: LvlFile | LvlType | LvlField | LvlService | LvlMethod | LvlEnumValue | LvlMiddleware | LvlErrorField,
		Doc:    "Marks the construct as deprecated; OpenAPI emits the deprecated flag.",
		Args:   ArgsRule{Min: 0, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"example": {
		Name:   "example",
		Levels: LvlType | LvlField | LvlMethod | LvlError | LvlErrorField,
		Doc:    "Single example value rendered in OpenAPI examples block. Argument may be a literal (string / int / float / bool) OR a `{k: v}` object.",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgAny}},
	},
	"examples": {
		Name:   "examples",
		Levels: LvlType | LvlField | LvlMethod | LvlError | LvlErrorField,
		Doc:    "Named map of example values rendered in OpenAPI.",
		// Validated by [analyzer.checkExamplesArgs].
	},
	"externalDocs": {
		Name:   "externalDocs",
		Levels: LvlType | LvlService | LvlMethod,
		Doc:    "External documentation URL surfaced in OpenAPI externalDocs.",
		// Validated by [analyzer.checkExternalDocsArgs] - string OR
		// {url: ..., description: ...} object.
	},

	// ---- OpenAPI file-header metadata ----
	// Per ast.File comment, file-level decorators carry top-of-file
	// OpenAPI metadata when no design-yaml override is supplied. Not in
	// the README §"Decorator compatibility matrix" table - kept here as
	// the runtime / fixtures rely on them.
	"title": {
		Name:   "title",
		Levels: LvlFile,
		Doc:    "OpenAPI document title (overrides craftgo.design.yaml openapi.title).",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
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
		Doc:    "At least one of the listed fields must be present.",
		Args: ArgsRule{
			Min: 1, Max: -1, Variadic: ArgStringOrIdent,
			AllowArrayShortcut: true,
		},
	},
	"mutuallyExclusive": {
		Name:   "mutuallyExclusive",
		Levels: LvlType,
		Doc:    "At most one of the listed fields may be present.",
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
		Doc:       "Exact or [min,max] length for strings.",
		Args:      ArgsRule{Min: 1, Max: 2, Kinds: []ArgKind{ArgInt, ArgInt}},
		AppliesTo: PrimString,
	},
	"minLength": {
		Name: "minLength", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Minimum string length.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo: PrimString,
	},
	"maxLength": {
		Name: "maxLength", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Maximum string length.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo: PrimString,
	},
	"pattern": {
		Name: "pattern", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "RE2 regex the value must match.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
		AppliesTo: PrimString,
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
		AppliesTo: PrimString,
	},

	// ---- Field validation: number ----
	"gt": {
		Name: "gt", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Value must be strictly greater than N (x > N).",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo: PrimNumber,
	},
	"gte": {
		Name: "gte", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Value must be greater than or equal to N (x >= N). Replaces @min.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo: PrimNumber,
	},
	"lt": {
		Name: "lt", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Value must be strictly less than N (x < N).",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo: PrimNumber,
	},
	"lte": {
		Name: "lte", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Value must be less than or equal to N (x <= N). Replaces @max.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo: PrimNumber,
	},
	"range": {
		Name: "range", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Numeric range [min, max] — both bounds inclusive.",
		Args:      ArgsRule{Min: 2, Max: 2, Kinds: []ArgKind{ArgNumber, ArgNumber}},
		AppliesTo: PrimNumber,
	},
	"positive": {Name: "positive", Levels: LvlField | LvlScalar | LvlErrorField, Doc: "Value must be > 0.", AppliesTo: PrimNumber, Flag: true},
	"negative": {Name: "negative", Levels: LvlField | LvlScalar | LvlErrorField, Doc: "Value must be < 0.", AppliesTo: PrimNumber, Flag: true},
	"multipleOf": {
		Name: "multipleOf", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:       "Value must be a multiple of N.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo: PrimNumber,
	},

	// ---- Field validation: array / map ----
	"minItems": {
		Name: "minItems", Levels: LvlField | LvlErrorField,
		Doc:       "Minimum array / map length.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo: PrimArray,
	},
	"maxItems": {
		Name: "maxItems", Levels: LvlField | LvlErrorField,
		Doc:       "Maximum array / map length.",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo: PrimArray,
	},
	"uniqueItems": {Name: "uniqueItems", Levels: LvlField | LvlErrorField, Doc: "Array elements must be unique.", AppliesTo: PrimArray, Flag: true},

	// ---- Field validation: file ----
	"maxSize": {
		Name: "maxSize", Levels: LvlField,
		Doc:       "Upload size cap (bytes / KB / MB / GB).",
		Args:      ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgSize}},
		AppliesTo: PrimFile,
	},
	"mimeTypes": {
		Name: "mimeTypes", Levels: LvlField,
		Doc:       "Allowed Content-Type list for uploads.",
		Args:      ArgsRule{Min: 1, Max: -1, Variadic: ArgString, AllowArrayShortcut: true},
		AppliesTo: PrimFile,
	},

	// ---- Field metadata ----
	"default": {
		Name: "default", Levels: LvlField | LvlErrorField,
		Doc:  "Default value applied when field absent.",
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgAny}},
	},
	"nullable": {Name: "nullable", Levels: LvlField | LvlErrorField, Doc: "Marks the field as accepting an explicit JSON null.", Flag: true},
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
	"group": {
		Name: "group", Levels: LvlService,
		Doc:  "Logical grouping label for OpenAPI tags & router buckets.",
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"middlewares": {
		Name:   "middlewares",
		Levels: LvlService | LvlMethod,
		Doc:    "Apply named middlewares; method-level appends to service-level chain.",
		Args:   ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true},
	},
	"tags": {
		Name:   "tags",
		Levels: LvlService | LvlMethod,
		Doc:    "OpenAPI tags. Method-level overrides service-level.",
		Args:   ArgsRule{Min: 1, Max: -1, Variadic: ArgStringOrIdent, AllowArrayShortcut: true},
	},
	"security": {
		Name:   "security",
		Levels: LvlService | LvlMethod,
		Doc:    "Security scheme requirement (OpenAPI metadata, not enforcement).",
		// Validated by [analyzer.checkSecurityArgs] - first positional
		// arg is the scheme ident (or `noauth`), with optional named
		// `scopes: [...]`.
	},

	// ---- Method-only ----
	"summary":     {Name: "summary", Levels: LvlMethod, Doc: "One-line OpenAPI operation summary.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}}},
	"operationId": {Name: "operationId", Levels: LvlMethod, Doc: "Override OpenAPI operationId.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}}},
	"errors":      {Name: "errors", Levels: LvlMethod, Doc: "Declared error responses for OpenAPI.", Args: ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true}},
	"status":      {Name: "status", Levels: LvlMethod, Doc: "Override default success status code.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}}},
	// NOTE: `@consumes`, `@produces`, `@accepts` were removed in
	// favour of a JSON-only v1 codec surface. craftgo's transport
	// layer hardcodes `application/json` for both request decode
	// and response encode; the decorators previously parsed but
	// produced no runtime / spec effect, so authors had no way to
	// realise they were silently dropped. Multi-codec content
	// negotiation is a planned future feature — when it lands the
	// decorators come back paired with a real `CodecRegistry`
	// dispatch path. Until then keep the surface small and honest.

	// ---- Method behavior ----
	"passthrough": {Name: "passthrough", Levels: LvlMethod, Doc: "Bypass framework parsing - logic receives the raw http.ResponseWriter and *http.Request and writes the response directly.", Flag: true},

	// ---- Method limits ----
	// `@timeout` caps the full handler lifecycle (decode body → user
	// logic → encode response) via `http.TimeoutHandler`. Independent
	// from transport-level deadlines (`http.Server.ReadTimeout` /
	// `WriteTimeout`) which the user configures on the server itself
	// when the stdlib defaults are not enough.
	"timeout":     {Name: "timeout", Levels: LvlMethod, Doc: "Cap the handler's execution time. Returns 503 + cancels context when the deadline elapses.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgDuration}}},
	"maxBodySize": {Name: "maxBodySize", Levels: LvlMethod, Doc: "Cap the request body size in bytes. Reads past the cap surface as a normal Read error which the JSON decoder maps to 400.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgSize}}},
}

// Lookup returns the [Spec] for `name` and whether it is registered.
// Convenience wrapper kept exported so the LSP / CLI can introspect the
// registry without poking the bare map.
func Lookup(name string) (Spec, bool) {
	s, ok := Registry[name]
	return s, ok
}

// sensitiveConflicts lists every decorator whose semantics contradict
// `@sensitive`. Validators are pointless because the field never
// crosses the wire (nothing to validate, nothing to constrain).
// Bindings (`@path`, `@query`, `@header`, `@cookie`, `@form`, `@body`)
// contradict the "server-internal only" intent. `@nullable` and
// `@default` shape wire behaviour that can't apply.
//
// Lives next to [Registry] because it's per-decorator metadata; the
// check that consumes it lives in semantic.go alongside the other
// small horizontal decorator checks.
var sensitiveConflicts = map[string]bool{
	"length":            true,
	"minLength":         true,
	"maxLength":         true,
	"pattern":           true,
	"format":            true,
	"gt":                true,
	"gte":               true,
	"lt":                true,
	"lte":               true,
	"range":             true,
	"positive":          true,
	"negative":          true,
	"multipleOf":        true,
	"minItems":          true,
	"maxItems":          true,
	"uniqueItems":       true,
	"requiresOneOf":     true,
	"mutuallyExclusive": true,
	"nullable":          true,
	"default":           true,
	"path":              true,
	"query":             true,
	"header":            true,
	"cookie":            true,
	"form":              true,
	"body":              true,
}
