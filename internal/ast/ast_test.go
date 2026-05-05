package ast

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// AST nodes carry a small but varied set of marker / accessor methods
// (declNode, typeMember, exprNode for the union tags; DeclName /
// DeclPos / MemberPos / ExprPos for position introspection;
// QualifiedIdent.String for printing). The producers - parser and
// semantic - only ever call them indirectly, so go's branch-coverage
// reports them as 0% even though the rest of the test suite proves the
// types are wired correctly. This file pins direct, exhaustive calls
// so the coverage gate stays green and any future shape change to a
// node fails here first instead of in a downstream consumer.

// nodePos is a small constant used everywhere we need a Pos value but
// don't care about its actual contents.
var nodePos = lexer.Position{Filename: "ast_test.go", Line: 1, Column: 1}

// TestDeclMarkers exercises each Decl implementation's marker +
// accessor surface. The marker (declNode) is private so the test
// has to live inside this package.
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
			c.d.declNode() // marker call - pins the method exists
			if got := c.d.DeclName(); got != c.want {
				t.Errorf("DeclName = %q, want %q", got, c.want)
			}
			if got := c.d.DeclPos(); got != c.pos {
				t.Errorf("DeclPos = %v, want %v", got, c.pos)
			}
		})
	}
}

// TestTypeMemberMarkers exercises the Field / Mixin marker pair plus
// MemberPos. Both implement the same interface; the test confirms each
// reports back the position it was constructed with.
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
			c.m.typeMember() // marker
			if got := c.m.MemberPos(); got != nodePos {
				t.Errorf("MemberPos = %v, want %v", got, nodePos)
			}
		})
	}
}

// TestExprMarkers exercises every Expr implementation. Each literal
// type carries a Pos field; the test asserts ExprPos round-trips it
// faithfully and the marker is callable.
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
			c.e.exprNode() // marker
			if got := c.e.ExprPos(); got != nodePos {
				t.Errorf("ExprPos = %v, want %v", got, nodePos)
			}
		})
	}
}

// TestQualifiedIdentString covers the dotted-form renderer for both
// single-segment and multi-segment names - the latter is the path
// used by cross-package references like `shared.User`.
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
