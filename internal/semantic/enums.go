package semantic

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// The enum value model: what an enum and its members carry on the wire,
// independent of the language that renders them. Targets read these
// instead of switching on [ast.EnumValueKind] themselves, so an
// int-backed member cannot stringify one way in the generated Go and
// another in the OpenAPI and AsyncAPI schemas.
//
// These read an enum that has passed analysis. [CodeEnumMixedTypes]
// rejects a mixed-kind enum, which is what lets [EnumPrimitive] decide
// the whole enum from its first member.

// EnumPrimitive returns the DSL primitive an enum's values lower to:
// `int` for an int-valued enum, `string` otherwise.
func EnumPrimitive(ed *ast.EnumDecl) string {
	if ed == nil {
		return "string"
	}
	values := ed.EnumValues()
	if len(values) > 0 && values[0].Kind == ast.EnumInt {
		return "int"
	}
	return "string"
}

// EnumMemberWire returns a member's typed wire value: int64 for an
// int-valued member, string otherwise. A member declared without a value
// carries its own DSL name.
func EnumMemberWire(v *ast.EnumValue) any {
	switch v.Kind {
	case ast.EnumInt:
		return v.IntValue
	case ast.EnumString:
		return v.StrValue
	default:
		return v.Name
	}
}

// EnumMemberInt returns a member's integer wire value, and false when the
// member is string-backed.
func EnumMemberInt(v *ast.EnumValue) (int64, bool) {
	n, ok := EnumMemberWire(v).(int64)
	return n, ok
}

// EnumMemberWireString returns a member's wire value in string form, as it
// appears as a JSON object key or a propertyNames entry - an int-backed
// member stringifies to its decimal form because JSON keys are strings.
func EnumMemberWireString(v *ast.EnumValue) string {
	if n, ok := EnumMemberInt(v); ok {
		return strconv.FormatInt(n, 10)
	}
	s, _ := EnumMemberWire(v).(string)
	return s
}

// EnumKind returns the value kind an enum lowers to, derived from
// [EnumPrimitive] so the Go base type, the schema type and the event key
// conversion cannot disagree.
func EnumKind(ed *ast.EnumDecl) ast.EnumValueKind {
	if EnumPrimitive(ed) == "int" {
		return ast.EnumInt
	}
	return ast.EnumString
}
