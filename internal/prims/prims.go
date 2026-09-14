// Package prims is the catalogue of the DSL's built-in type spellings. One
// row per name carries every fact the analyser, the code generators, and
// the language server read about it, so a built-in is described once. Each
// output gets its own column (Go, the OpenAPI pair); adding a target
// language adds a column here rather than a second mapping table.
package prims

// Kind classifies a built-in by the values it holds.
type Kind uint8

const (
	String Kind = iota + 1
	Bool
	Int
	Uint
	Float
	Bytes    // raw byte buffer
	Any      // opaque JSON value
	File     // multipart upload
	Object   // bag of fields, valid only inside `@example({...})`
	DateTime // RFC 3339 timestamp
)

// Spec describes one built-in type.
type Spec struct {
	Name string
	Kind Kind
	// Bits is the width of a sized integer or float (8, 16, 32, 64); 0
	// for the platform-sized `int` / `uint` and for non-numeric kinds.
	Bits int
	// Go is the Go type the name lowers to; "" when it has none.
	Go string
	// Parser is the strconv function a wire-string binder parses the
	// value with; "" for `string` (used verbatim) and for kinds with no
	// wire form.
	Parser string
	// OASType and OASFormat are the OpenAPI schema `type` and `format`;
	// an empty OASType is an unconstrained schema.
	OASType, OASFormat string
	// Lo and Hi bound the values an integer kind can hold.
	Lo, Hi float64
	// Doc is the hover text the language server shows.
	Doc string
}

var specs = []Spec{
	{Name: "string", Kind: String, Go: "string", OASType: "string", Doc: "**`string`** - UTF-8 text primitive."},
	{Name: "bool", Kind: Bool, Go: "bool", Parser: "strconv.ParseBool", OASType: "boolean", Doc: "**`bool`** - boolean primitive (`true` / `false`)."},
	{Name: "int", Kind: Int, Go: "int", Parser: "strconv.ParseInt", OASType: "integer", Lo: -9223372036854775808, Hi: 9223372036854775807, Doc: "**`int`** - platform-sized signed integer."},
	{Name: "int8", Kind: Int, Bits: 8, Go: "int8", Parser: "strconv.ParseInt", OASType: "integer", Lo: -128, Hi: 127, Doc: "**`int8`** - 8-bit signed integer."},
	{Name: "int16", Kind: Int, Bits: 16, Go: "int16", Parser: "strconv.ParseInt", OASType: "integer", Lo: -32768, Hi: 32767, Doc: "**`int16`** - 16-bit signed integer."},
	{Name: "int32", Kind: Int, Bits: 32, Go: "int32", Parser: "strconv.ParseInt", OASType: "integer", OASFormat: "int32", Lo: -2147483648, Hi: 2147483647, Doc: "**`int32`** - 32-bit signed integer."},
	{Name: "int64", Kind: Int, Bits: 64, Go: "int64", Parser: "strconv.ParseInt", OASType: "integer", OASFormat: "int64", Lo: -9223372036854775808, Hi: 9223372036854775807, Doc: "**`int64`** - 64-bit signed integer."},
	{Name: "uint", Kind: Uint, Go: "uint", Parser: "strconv.ParseUint", OASType: "integer", Lo: 0, Hi: 18446744073709551615, Doc: "**`uint`** - platform-sized unsigned integer."},
	{Name: "uint8", Kind: Uint, Bits: 8, Go: "uint8", Parser: "strconv.ParseUint", OASType: "integer", Lo: 0, Hi: 255, Doc: "**`uint8`** - 8-bit unsigned integer."},
	{Name: "uint16", Kind: Uint, Bits: 16, Go: "uint16", Parser: "strconv.ParseUint", OASType: "integer", Lo: 0, Hi: 65535, Doc: "**`uint16`** - 16-bit unsigned integer."},
	{Name: "uint32", Kind: Uint, Bits: 32, Go: "uint32", Parser: "strconv.ParseUint", OASType: "integer", Lo: 0, Hi: 4294967295, Doc: "**`uint32`** - 32-bit unsigned integer."},
	{Name: "uint64", Kind: Uint, Bits: 64, Go: "uint64", Parser: "strconv.ParseUint", OASType: "integer", Lo: 0, Hi: 18446744073709551615, Doc: "**`uint64`** - 64-bit unsigned integer."},
	{Name: "float32", Kind: Float, Bits: 32, Go: "float32", Parser: "strconv.ParseFloat", OASType: "number", OASFormat: "float", Doc: "**`float32`** - 32-bit IEEE-754 float."},
	{Name: "float64", Kind: Float, Bits: 64, Go: "float64", Parser: "strconv.ParseFloat", OASType: "number", OASFormat: "double", Doc: "**`float64`** - 64-bit IEEE-754 float."},
	{Name: "bytes", Kind: Bytes, Go: "[]byte", OASType: "string", OASFormat: "byte", Doc: "**`bytes`** - raw byte buffer.\n\nGenerates `[]byte` in Go."},
	{Name: "any", Kind: Any, Go: "any", Doc: "**`any`** - opaque JSON value.\n\nGenerates `any` in Go."},
	{Name: "datetime", Kind: DateTime, Go: "time.Time", OASType: "string", OASFormat: "date-time", Doc: "**`datetime`** - an RFC 3339 timestamp.\n\nGenerates `time.Time` in Go and travels as an RFC 3339 string in JSON. A body field only: it cannot be bound from a query, header, cookie or form value."},
	{Name: "file", Kind: File, Go: "*multipart.FileHeader", OASType: "string", OASFormat: "binary", Doc: "**`file`** - multipart file upload (request only, must be paired with `@form`).\n\nGenerates `*multipart.FileHeader`."},
	{Name: "object", Kind: Object},
}

var byName = func() map[string]Spec {
	m := make(map[string]Spec, len(specs))
	for _, s := range specs {
		m[s.Name] = s
	}
	return m
}()

// All returns every built-in in catalogue order.
func All() []Spec { return specs }

// Lookup returns the spec for name and whether name is a built-in.
func Lookup(name string) (Spec, bool) {
	s, ok := byName[name]
	return s, ok
}

// Is reports whether name is a built-in type spelling.
func Is(name string) bool {
	_, ok := byName[name]
	return ok
}

// IsWireParseable reports whether a wire-string binder (`@query`,
// `@header`, `@cookie`, `@form`) can parse name from a single string:
// string, bool, and the integer and float kinds.
func IsWireParseable(name string) bool {
	switch byName[name].Kind {
	case String, Bool, Int, Uint, Float:
		return true
	}
	return false
}

// IsNumeric reports whether name is an integer or float kind.
func IsNumeric(name string) bool {
	switch byName[name].Kind {
	case Int, Uint, Float:
		return true
	}
	return false
}

// IsInteger reports whether name is a signed or unsigned integer kind.
func IsInteger(name string) bool {
	switch byName[name].Kind {
	case Int, Uint:
		return true
	}
	return false
}

// IsUnsigned reports whether name is an unsigned integer kind.
func IsUnsigned(name string) bool { return byName[name].Kind == Uint }

// Capacity returns the value range an integer kind can hold; ok is false
// for every other name.
func Capacity(name string) (lo, hi float64, ok bool) {
	s := byName[name]
	if s.Kind != Int && s.Kind != Uint {
		return 0, 0, false
	}
	return s.Lo, s.Hi, true
}
