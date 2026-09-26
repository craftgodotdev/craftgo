package semantic

import (
	"maps"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

// Level is a bitmask of the declaration sites a decorator may appear at.
type Level uint32

const (
	// LvlFile is the file header, before `package`.
	LvlFile Level = 1 << iota
	// LvlType is a `type Name { ... }` declaration.
	LvlType
	// LvlField is a field inside a `type` body.
	LvlField
	// LvlService is a primary `service Name { ... }` declaration.
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
	// LvlEvent is an `event Name { ... }` declaration.
	LvlEvent
	// LvlErrorField is a field inside an `error` body; it refuses the
	// request-only decorators LvlField accepts.
	LvlErrorField
)

// levelNames labels each single-bit level, in the order [Level.String]
// lists them.
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
	{LvlErrorField, "error field"},
}

// Name returns the label of a single-bit level, or "unknown" for zero or a
// multi-bit mask.
func (l Level) Name() string {
	for _, e := range levelNames {
		if l == e.bit {
			return e.name
		}
	}
	return "unknown"
}

// String joins the labels of every set bit with ", ", or returns "(none)"
// for zero.
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

// ArgKind is the literal shape expected at a positional argument slot.
type ArgKind uint8

const (
	// ArgAny accepts any expression.
	ArgAny ArgKind = iota
	// ArgString matches a [ast.StringLit] (regular or raw).
	ArgString
	// ArgInt matches a [ast.IntLit].
	ArgInt
	// ArgNumber matches an int or float literal.
	ArgNumber
	// ArgBool matches a [ast.BoolLit].
	ArgBool
	// ArgIdent matches a bare identifier ([ast.IdentExpr]).
	ArgIdent
	// ArgDuration matches a [ast.DurationLit] (`5s`, `100ms`, ...).
	ArgDuration
	// ArgSize matches a [ast.SizeLit] (`1MB`, `8KB`, ...).
	ArgSize
	// ArgStringOrIdent matches a string literal or a bare identifier.
	ArgStringOrIdent
)

// argKinds gives each ArgKind the label its diagnostics use and the
// expression kinds, as [exprKind] names them, it accepts; a bare int is also
// a duration (seconds) or a size (bytes).
var argKinds = map[ArgKind]struct {
	label   string
	accepts []string
}{
	ArgString:        {"string", []string{"string"}},
	ArgInt:           {"int", []string{"int"}},
	ArgNumber:        {"int or float", []string{"int", "float"}},
	ArgBool:          {"bool", []string{"bool"}},
	ArgIdent:         {"identifier", []string{"identifier"}},
	ArgDuration:      {"duration", []string{"duration", "int"}},
	ArgSize:          {"size", []string{"size", "int"}},
	ArgStringOrIdent: {"string or identifier", []string{"string", "identifier"}},
}

// String returns the label used in "expected X, got Y" diagnostics.
func (k ArgKind) String() string {
	if e, ok := argKinds[k]; ok {
		return e.label
	}
	return "any"
}

// ArgsRule is the positional argument shape of a decorator.
type ArgsRule struct {
	// Min is the fewest positional arguments allowed.
	Min int
	// Max is the most positional arguments allowed; -1 means unbounded.
	Max int
	// Kinds is the expected kind per position.
	Kinds []ArgKind
	// Variadic is the kind of every argument past len(Kinds).
	Variadic ArgKind
	// Enum, when non-empty, is the set the first argument's string or
	// identifier value must belong to.
	Enum []string
	// AllowArrayShortcut accepts one array literal in place of the argument
	// list, checking its elements against Min, Max and Variadic.
	AllowArrayShortcut bool
}

// Prims is a bitmask of the primitive type categories a decorator applies to.
type Prims uint16

const (
	// PrimString covers `string`, and scalars over it.
	PrimString Prims = 1 << iota
	// PrimBytes covers `bytes`, and scalars over it.
	PrimBytes
	// PrimInteger covers the signed and unsigned integers.
	PrimInteger
	// PrimFloat covers `float32` and `float64`.
	PrimFloat
	// PrimBool covers `bool`.
	PrimBool
	// PrimArray covers arrays.
	PrimArray
	// PrimMap covers maps.
	PrimMap
	// PrimFile covers the `file` primitive (multipart upload).
	PrimFile
	// PrimDateTime covers `datetime`, which no validator targets.
	PrimDateTime
	// PrimRawBytes covers a `bytes @format(raw)` field, which only that
	// `@format` targets.
	PrimRawBytes
	// PrimStruct covers a struct type, which no validator targets.
	PrimStruct
	// PrimAny matches any field type.
	PrimAny Prims = 0
	// PrimNumber covers integers and floats.
	PrimNumber = PrimInteger | PrimFloat
)

// primNames labels each category, in the order [Prims.String] lists them;
// [PrimNumber] reads as one.
var primNames = []struct {
	cats Prims
	name string
}{
	{PrimString, "string"},
	{PrimBytes, "bytes"},
	{PrimNumber, "number"},
	{PrimInteger, "integer"},
	{PrimFloat, "float"},
	{PrimBool, "bool"},
	{PrimArray, "array"},
	{PrimMap, "map"},
	{PrimFile, "file"},
	{PrimDateTime, "datetime"},
	{PrimRawBytes, "bytes @format(raw)"},
	{PrimStruct, "struct"},
}

// String joins the category names with ", ", or returns "any" for zero.
func (p Prims) String() string {
	if p == 0 {
		return "any"
	}
	var parts []string
	for _, e := range primNames {
		if p&e.cats == e.cats {
			parts = append(parts, e.name)
			p &^= e.cats
		}
	}
	return strings.Join(parts, ", ")
}

// ConstraintFamily classifies what a constraint decorator restricts.
type ConstraintFamily uint8

const (
	// ConstraintNumeric bounds a number.
	ConstraintNumeric ConstraintFamily = 1 << iota
	// ConstraintLength bounds text length.
	ConstraintLength
	// ConstraintText restricts text shape.
	ConstraintText
	// ConstraintItems bounds a collection.
	ConstraintItems
	// ConstraintRuntime is checked at runtime on a multipart part and has
	// no schema form.
	ConstraintRuntime
)

// ConstraintNarrowing is every family a field may add to a referenced type;
// a $ref is never a collection or a file.
const ConstraintNarrowing = ConstraintNumeric | ConstraintLength | ConstraintText

// ConstraintSchema is every family with a schema form.
const ConstraintSchema = ConstraintNumeric | ConstraintLength | ConstraintText | ConstraintItems

// Spec describes one decorator: where it may appear, its hover doc and its
// argument shape.
type Spec struct {
	// Name is the decorator name without the `@`.
	Name string
	// Levels is every site where the decorator is legal.
	Levels Level
	// Doc is the LSP hover text.
	Doc string
	// Args is the positional argument shape; the zero value takes no
	// arguments, which makes the decorator a flag: empty `()` on it draws
	// [CodeFlagEmptyParens].
	Args ArgsRule
	// AppliesTo is the set of primitive categories the decorated field or
	// scalar may have; zero allows any.
	AppliesTo Prims
	// Constraint is what the decorator restricts; zero means it is not a
	// constraint.
	Constraint ConstraintFamily
	// Repeatable lets the decorator appear more than once on one site, each
	// occurrence adding to the aggregate.
	Repeatable bool
	// Metadata marks a decorator that only documents its target; every
	// other field decorator conflicts with `@sensitive`.
	Metadata bool
}

// FormatRaw is the `@format` value that checks nothing: the `bytes` field it
// marks already holds the encoded value, which the codec embeds verbatim.
const FormatRaw = "raw"

// FormatRawDoc is the hover text for `@format(raw)`.
const FormatRawDoc = "**`@format(raw)`** - the bytes ARE the value, in the message's own encoding.\n\n" +
	"Only on a `bytes` field. The codec embeds them untouched instead of base64-encoding the buffer, so what a producer wrote is what a consumer reads - an explicit `null`, an integer past 2^53 and a trailing zero such as `1.50` all survive, none of which does through `any`. Generates `wire.Raw` in Go - in every shape: nil is the absent value and an explicit `null` is the four bytes `null`, so `?` only omits an absent value and `@nullable` only keeps the key. A body field only; no other validator applies and `@default` is refused."

// HasRawFormat reports whether decs carries `@format(raw)`.
func HasRawFormat(decs []*ast.Decorator) bool {
	return isFormatRaw(ast.FindDecorator(decs, "format"))
}

// isFormatRaw reports whether d is `@format(raw)`.
func isFormatRaw(d *ast.Decorator) bool {
	if d == nil || d.Name != "format" || len(d.Args) == 0 {
		return false
	}
	name, _ := ast.TextValue(d.Args[0].Value)
	return name == FormatRaw
}

var formatValues = append(strfmt.Names(), FormatRaw)

// registry is the closed set of decorators craftgo recognises; any other
// `@name` is an error. [DecoratorSpec] and [Names] read it.
var registry = map[string]Spec{
	// ---- Universal documentation / lifecycle ----
	"doc": {
		Name:     "doc",
		Levels:   LvlFile | LvlType | LvlField | LvlService | LvlMethod | LvlEnum | LvlEnumValue | LvlError | LvlScalar | LvlMiddleware | LvlEvent | LvlErrorField,
		Doc:      "Free-form documentation: the OpenAPI description and the generated Go doc comment, in place of the // comment block.",
		Args:     ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
		Metadata: true,
	},
	"deprecated": {
		Name:     "deprecated",
		Levels:   LvlFile | LvlType | LvlField | LvlService | LvlMethod | LvlEnumValue | LvlMiddleware | LvlEvent | LvlErrorField,
		Doc:      "Marks the construct as deprecated; OpenAPI emits the deprecated flag.",
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
	"length": {
		Name: "length", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Exact or [min,max] length for strings.",
		Args:       ArgsRule{Min: 1, Max: 2, Kinds: []ArgKind{ArgInt, ArgInt}},
		AppliesTo:  PrimString | PrimBytes,
		Constraint: ConstraintLength,
	},
	"minLength": {
		Name: "minLength", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Minimum string length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimString | PrimBytes,
		Constraint: ConstraintLength,
	},
	"maxLength": {
		Name: "maxLength", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Maximum string length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimString | PrimBytes,
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
		Doc:    "Named format constraint (e.g. email, uuid, datetime). `raw`, on a `bytes` field only, says the bytes are the encoded value and the codec embeds them untouched.",
		Args: ArgsRule{
			Min: 1, Max: 1,
			Kinds: []ArgKind{ArgStringOrIdent},
			Enum:  formatValues,
		},
		AppliesTo:  PrimString | PrimBytes | PrimRawBytes,
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
	"positive": {Name: "positive", Levels: LvlField | LvlScalar | LvlErrorField, Doc: "Value must be > 0.", AppliesTo: PrimNumber, Constraint: ConstraintNumeric},
	"negative": {Name: "negative", Levels: LvlField | LvlScalar | LvlErrorField, Doc: "Value must be < 0.", AppliesTo: PrimNumber, Constraint: ConstraintNumeric},
	"multipleOf": {
		Name: "multipleOf", Levels: LvlField | LvlScalar | LvlErrorField,
		Doc:        "Value must be a multiple of N.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgNumber}},
		AppliesTo:  PrimInteger,
		Constraint: ConstraintNumeric,
	},

	// ---- Field validation: array / map ----
	"minItems": {
		Name: "minItems", Levels: LvlField | LvlErrorField,
		Doc:        "Minimum array / map length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimArray | PrimMap,
		Constraint: ConstraintItems,
	},
	"maxItems": {
		Name: "maxItems", Levels: LvlField | LvlErrorField,
		Doc:        "Maximum array / map length.",
		Args:       ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}},
		AppliesTo:  PrimArray | PrimMap,
		Constraint: ConstraintItems,
	},
	"uniqueItems": {Name: "uniqueItems", Levels: LvlField | LvlErrorField, Doc: "Array elements must be unique.", AppliesTo: PrimArray, Constraint: ConstraintItems},

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
	"nullable": {Name: "nullable", Levels: LvlField | LvlErrorField, Doc: "Marks the field as accepting an explicit JSON null."},
	"json": {
		Name: "json", Levels: LvlField | LvlErrorField,
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
		Doc:  "Sets the JSON key of a body field when it is not the field name - a contract another system owns, or a key the parser reads as a mixin (`OrderItem Item[]`). The Go struct tag, the documents and validation messages all use it. Not for a field bound off the body (@path / @query / @header / @cookie / @form), which names its own wire location.",
	},
	"sensitive": {
		Name: "sensitive", Levels: LvlField | LvlErrorField,
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
		Name:   decoratorContract,
		Levels: LvlEvent,
		Doc:    "Overrides the event's wire identity. Defaults to `<package>.<Event>`; set it to interoperate with a contract another system already publishes.",
		Args:   ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"group": {
		Name: "group", Levels: LvlService,
		Doc:  "Writes the generated handlers, service stubs and routes of the block's methods under <group>/ in place of the service's own directory, and adds its value as an OpenAPI tag on every method; does not affect the route or OpenAPI path. Accepts a nested path like \"admin/ops\".",
		Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}},
	},
	"middlewares": {
		Name:       "middlewares",
		Levels:     LvlService | LvlMethod,
		Doc:        "Apply named middlewares; method-level appends to service-level chain. Args: variadic idents or a single array literal.",
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
		Levels: LvlMethod,
		Doc:    "Clear the inherited @middlewares chain on this method. The method's own decorator then starts from empty instead of appending to the service-level chain. On an extend service block, every method of the block drops the primary service's chain.",
		Args:   ArgsRule{Min: 0, Max: 0},
	},
	"ignoreSecurity": {
		Name:   "ignoreSecurity",
		Levels: LvlMethod,
		Doc:    "Clear the inherited @security chain on this method. Useful for public endpoints inside an otherwise-authenticated service. On an extend service block, every method of the block drops the primary service's chain.",
		Args:   ArgsRule{Min: 0, Max: 0},
	},
	"ignoreTags": {
		Name:   "ignoreTags",
		Levels: LvlMethod,
		Doc:    "Clear the inherited @tags list on this method. Method-level @tags(...) then start from empty. On an extend service block, every method of the block drops the primary service's list.",
		Args:   ArgsRule{Min: 0, Max: 0},
	},

	// ---- Method-only ----
	"summary":     {Name: "summary", Levels: LvlMethod, Doc: "One-line OpenAPI operation summary.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}}},
	"operationId": {Name: "operationId", Levels: LvlMethod, Doc: "Override OpenAPI operationId.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgString}}},
	"errors":      {Name: "errors", Levels: LvlMethod, Doc: "Declared error responses for OpenAPI. Args: variadic error idents or a single array literal.", Args: ArgsRule{Min: 1, Max: -1, Variadic: ArgIdent, AllowArrayShortcut: true}, Repeatable: true},
	"status":      {Name: "status", Levels: LvlMethod, Doc: "Override default success status code.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgInt}}},

	// ---- Method behavior ----
	"passthrough": {Name: "passthrough", Levels: LvlMethod, Doc: "Hand both transport sides to logic: the entry point receives the raw http.ResponseWriter and *http.Request and writes the response directly. Equivalent to @rawRequest @rawResponse. Optional request/response blocks are a docs-only contract (OpenAPI + generated types)."},
	"rawRequest":  {Name: "rawRequest", Levels: LvlMethod, Doc: "Hand the request side to logic: the entry point receives the raw *http.Request and the framework skips request bind + validate. A request block is a docs-only contract (OpenAPI + generated type). The response stays framework-encoded unless @rawResponse is also set."},
	"rawResponse": {Name: "rawResponse", Levels: LvlMethod, Doc: "Hand the response side to logic: the entry point receives the http.ResponseWriter and writes status, headers and body itself; the framework skips the response encode. A response block is a docs-only contract (OpenAPI + generated type). The request stays bound + validated unless @rawRequest is also set."},

	// ---- Method limits ----
	"timeout":     {Name: "timeout", Levels: LvlMethod, Doc: "Cap the handler's execution time: the request context is cancelled when the deadline elapses (no status is written automatically). Overrides the global handlerTimeout for this route.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgDuration}}},
	"maxBodySize": {Name: "maxBodySize", Levels: LvlMethod, Doc: "Cap the request body size in bytes: a declared Content-Length over the cap and a read past it both answer 413. Multipart parsers also lift their in-memory budget to this value.", Args: ArgsRule{Min: 1, Max: 1, Kinds: []ArgKind{ArgSize}}},
}

// DecoratorSpec returns the [Spec] registered under name, and whether there is one.
func DecoratorSpec(name string) (Spec, bool) {
	s, ok := registry[name]
	return s, ok
}

// Names returns the name of every registered decorator, sorted.
func Names() []string {
	return slices.Sorted(maps.Keys(registry))
}

// removed maps each removed decorator to its migration note.
var removed = map[string]string{
	"key": "@key was removed: which entity a message belongs to is decided when it is published, not by the contract. " +
		"Pass the key to the publish call instead - `orders.OrderPlaced.Publish(ctx, bus, payload, craftevents.WithKey(string(payload.OrderID)))`.",
	"consumerGroup": "@consumerGroup was removed: a group is where a consumer resumes on the broker, so it belongs to the deployable rather than to the shared design. " +
		"Name it where the bus is built and pass it to the subscription - `orders.Placed.Subscribe(bus, ordersGroup, h.Placed)`.",
	"consumeMiddlewares": "@consumeMiddlewares was removed, along with the `consume middleware Name` declaration: a subscription's chain is ordinary Go, built where the bus is. " +
		"Install one bus-wide with `bus.Use(retry, timeout)`, or set `Subscription.Chain` for a single registration.",
}

// RemovedDecorator returns the migration note for a removed decorator, and
// whether name is one.
func RemovedDecorator(name string) (string, bool) {
	msg, ok := removed[name]
	return msg, ok
}

// ConstraintOf returns the decorator's constraint classification, or zero
// when the name is unknown or the decorator is not a constraint.
func ConstraintOf(name string) ConstraintFamily {
	spec, ok := DecoratorSpec(name)
	if !ok {
		return 0
	}
	return spec.Constraint
}
