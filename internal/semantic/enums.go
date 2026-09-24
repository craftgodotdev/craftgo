package semantic

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// EnumPrimitive returns the DSL primitive an enum's values lower to: `int`
// for an int-valued enum, `string` otherwise. It reads the first member,
// since analysis rejects a mixed-kind enum.
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

// enumMemberInt returns a member's integer wire value, and false when the
// member is string-backed.
func enumMemberInt(v *ast.EnumValue) (int64, bool) {
	n, ok := EnumMemberWire(v).(int64)
	return n, ok
}

// EnumMemberWireString returns a member's wire value as a JSON object key:
// an int-backed member in decimal.
func EnumMemberWireString(v *ast.EnumValue) string {
	if n, ok := enumMemberInt(v); ok {
		return strconv.FormatInt(n, 10)
	}
	s, _ := EnumMemberWire(v).(string)
	return s
}

// EnumKind returns the value kind matching [EnumPrimitive].
func EnumKind(ed *ast.EnumDecl) ast.EnumValueKind {
	if EnumPrimitive(ed) == "int" {
		return ast.EnumInt
	}
	return ast.EnumString
}
