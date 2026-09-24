package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// Each enum value kind reads back its wire value, text and int.
func TestEnumMemberWire(t *testing.T) {
	cases := []struct {
		name      string
		v         ast.EnumValue
		wantWire  any
		wantText  string
		wantInt   int64
		wantIsInt bool
	}{
		{"bare carries its own name",
			ast.EnumValue{Name: "Low", Kind: ast.EnumBare}, "Low", "Low", 0, false},
		{"string carries its value",
			ast.EnumValue{Name: "On", Kind: ast.EnumString, StrValue: "on"}, "on", "on", 0, false},
		{"empty string stays empty, not the name",
			ast.EnumValue{Name: "Off", Kind: ast.EnumString, StrValue: ""}, "", "", 0, false},
		{"int stringifies to decimal",
			ast.EnumValue{Name: "Paid", Kind: ast.EnumInt, IntValue: 10}, int64(10), "10", 10, true},
		{"zero int is not absent",
			ast.EnumValue{Name: "Free", Kind: ast.EnumInt, IntValue: 0}, int64(0), "0", 0, true},
		{"negative int keeps its sign",
			ast.EnumValue{Name: "Neg", Kind: ast.EnumInt, IntValue: -5}, int64(-5), "-5", -5, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.v
			if got := EnumMemberWire(&v); got != c.wantWire {
				t.Errorf("EnumMemberWire = %#v, want %#v", got, c.wantWire)
			}
			if got := EnumMemberWireString(&v); got != c.wantText {
				t.Errorf("EnumMemberWireString = %q, want %q", got, c.wantText)
			}
			n, IsInt := enumMemberInt(&v)
			if IsInt != c.wantIsInt || n != c.wantInt {
				t.Errorf("enumMemberInt = (%d, %v), want (%d, %v)", n, IsInt, c.wantInt, c.wantIsInt)
			}
		})
	}
}

// EnumPrimitive takes the enum's primitive from its first member; mixed kinds are rejected earlier.
func TestEnumPrimitive(t *testing.T) {
	intEnum := &ast.EnumDecl{Members: []ast.EnumMember{
		&ast.EnumValue{Name: "A", Kind: ast.EnumInt, IntValue: 1},
	}}
	strEnum := &ast.EnumDecl{Members: []ast.EnumMember{
		&ast.EnumValue{Name: "A", Kind: ast.EnumString, StrValue: "a"},
	}}
	bareEnum := &ast.EnumDecl{Members: []ast.EnumMember{
		&ast.EnumValue{Name: "A", Kind: ast.EnumBare},
	}}
	for _, c := range []struct {
		name string
		ed   *ast.EnumDecl
		want string
	}{
		{"int", intEnum, "int"},
		{"string", strEnum, "string"},
		{"bare", bareEnum, "string"},
		{"empty", &ast.EnumDecl{}, "string"},
		{"nil", nil, "string"},
	} {
		if got := EnumPrimitive(c.ed); got != c.want {
			t.Errorf("EnumPrimitive(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}
