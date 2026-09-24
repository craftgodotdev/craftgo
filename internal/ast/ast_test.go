package ast

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

var nodePos = lexer.Position{Filename: "ast_test.go", Line: 1, Column: 1}

// TestDeclMarkers pins DeclName and DeclPos for the declaration kinds.
func TestDeclMarkers(t *testing.T) {
	cases := []struct {
		name string
		d    Decl
		want string
		pos  Pos
	}{
		{"TypeDecl", &TypeDecl{Pos: nodePos, Name: "Foo"}, "Foo", nodePos},
		{"EnumDecl", &EnumDecl{Pos: nodePos, Name: "Status"}, "Status", nodePos},
		{"ErrorDecl", &ErrorDecl{Pos: nodePos, Category: "NotFound", Name: "UserNotFound"}, "UserNotFound", nodePos},
		{"ScalarDecl", &ScalarDecl{Pos: nodePos, Name: "Email"}, "Email", nodePos},
		{"MiddlewareDecl", &MiddlewareDecl{Pos: nodePos, Name: "Auth"}, "Auth", nodePos},
		{"ServiceDecl", &ServiceDecl{Pos: nodePos, Name: "Users"}, "Users", nodePos},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.d.declNode()
			if got := c.d.DeclName(); got != c.want {
				t.Errorf("DeclName = %q, want %q", got, c.want)
			}
			if got := c.d.DeclPos(); got != c.pos {
				t.Errorf("DeclPos = %v, want %v", got, c.pos)
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
