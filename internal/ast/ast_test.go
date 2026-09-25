package ast

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

var (
	nodePos = lexer.Position{Filename: "ast_test.go", Line: 1, Column: 1}
	namePos = lexer.Position{Filename: "ast_test.go", Line: 1, Column: 6, Offset: 5}
)

// TestDeclMarkers pins DeclName, DeclPos and DeclNamePos for the declaration
// kinds.
func TestDeclMarkers(t *testing.T) {
	cases := []struct {
		name string
		d    Decl
		want string
	}{
		{"TypeDecl", &TypeDecl{Pos: nodePos, NamePos: namePos, Name: "Foo"}, "Foo"},
		{"EnumDecl", &EnumDecl{Pos: nodePos, NamePos: namePos, Name: "Status"}, "Status"},
		{"ErrorDecl", &ErrorDecl{Pos: nodePos, NamePos: namePos, Category: "NotFound", Name: "UserNotFound"}, "UserNotFound"},
		{"ScalarDecl", &ScalarDecl{Pos: nodePos, NamePos: namePos, Name: "Email"}, "Email"},
		{"MiddlewareDecl", &MiddlewareDecl{Pos: nodePos, NamePos: namePos, Name: "Auth"}, "Auth"},
		{"ServiceDecl", &ServiceDecl{Pos: nodePos, NamePos: namePos, Name: "Users"}, "Users"},
		{"EventDecl", &EventDecl{Pos: nodePos, NamePos: namePos, Name: "Placed"}, "Placed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.d.declNode()
			if got := c.d.DeclName(); got != c.want {
				t.Errorf("DeclName = %q, want %q", got, c.want)
			}
			if got := c.d.DeclPos(); got != nodePos {
				t.Errorf("DeclPos = %v, want %v", got, nodePos)
			}
			if got := c.d.DeclNamePos(); got != namePos {
				t.Errorf("DeclNamePos = %v, want %v", got, namePos)
			}
		})
	}
}

// TestTypeMemberMarkers pins MemberPos for fields and mixins.
func TestTypeMemberMarkers(t *testing.T) {
	cases := []struct {
		name string
		m    TypeMember
	}{
		{"Field", &Field{Pos: nodePos, Name: "x", Type: &TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{"string"}}}}}},
		{"Mixin", &Mixin{Pos: nodePos, Ref: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{"Profile"}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.m.typeMember()
			if got := c.m.MemberPos(); got != nodePos {
				t.Errorf("MemberPos = %v, want %v", got, nodePos)
			}
		})
	}
}

// TestExprMarkers pins ExprPos for every expression kind.
func TestExprMarkers(t *testing.T) {
	cases := []struct {
		name string
		e    Expr
	}{
		{"StringLit", &StringLit{Pos: nodePos, Value: "x"}},
		{"IntLit", &IntLit{Pos: nodePos, Value: 1}},
		{"FloatLit", &FloatLit{Pos: nodePos, Value: 1.5}},
		{"BoolLit", &BoolLit{Pos: nodePos, Value: true}},
		{"NullLit", &NullLit{Pos: nodePos}},
		{"DurationLit", &DurationLit{Pos: nodePos, Text: "5s"}},
		{"SizeLit", &SizeLit{Pos: nodePos, Text: "5MB"}},
		{"IdentExpr", &IdentExpr{Pos: nodePos, Name: &QualifiedIdent{Parts: []string{"Name"}}}},
		{"ArrayLit", &ArrayLit{Pos: nodePos}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.e.exprNode()
			if got := c.e.ExprPos(); got != nodePos {
				t.Errorf("ExprPos = %v, want %v", got, nodePos)
			}
		})
	}
}

// TestQualifiedIdentString pins the dotted form of single- and multi-part
// names.
func TestQualifiedIdentString(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"User"}, "User"},
		{[]string{"shared", "User"}, "shared.User"},
		{[]string{"a", "b", "C"}, "a.b.C"},
	}
	for _, c := range cases {
		q := &QualifiedIdent{Parts: c.parts}
		if got := q.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

// A type reference renders as the DSL spells it, generic arguments included.
func TestTypeRefString(t *testing.T) {
	named := func(name string, args ...*TypeRef) *TypeRef {
		return &TypeRef{Named: &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{name}}, Args: args}}
	}
	grid := named("int")
	grid.Array, grid.ArrayDepth = true, 2
	page := named("Page", named("User"), named("int"))
	page.Optional = true
	handBuilt := named("Item")
	handBuilt.Array = true
	cases := []struct {
		t    *TypeRef
		want string
	}{
		{nil, "?"},
		{grid, "int[][]"},
		{page, "Page<User, int>?"},
		{&TypeRef{Map: &MapType{Key: named("string"), Value: page}}, "map<string, Page<User, int>?>"},
		{handBuilt, "Item[]"},
	}
	for _, c := range cases {
		if got := c.t.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}
