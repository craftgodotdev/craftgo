// Package ast defines the abstract syntax tree produced by the parser.
//
// Every node carries a [Pos] (alias for [lexer.Position]) so diagnostics from
// later stages - semantic analysis, codegen, formatter, LSP - can map back to
// the originating source location. The AST is the single shared
// representation consumed by every downstream tool: keeping it small and
// strongly-typed lets the parser, semantic analyser, and codegen evolve
// independently.
package ast

import (
	"strings"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// Pos aliases [lexer.Position] to keep ast/* free of a hard dependency on
// lexer naming and to make node-position fields read clearly.
type Pos = lexer.Position

// astMarker is the body of every marker method (`declNode`,
// `typeMember`, `exprNode`). Marker methods exist only to seal a Go
// interface - they have no behaviour. An empty body (`{}`) would be
// idiomatic but produces 0 statements, which `go tool cover` reports
// as 0.0%-covered even when callers exercise the method. Calling a
// tiny named helper that performs one no-op assignment gives every
// marker a measurable statement so the 100% core-coverage CI gate
// stays satisfied.
func astMarker() { _ = astMarkerCalled }

// astMarkerCalled is the throwaway sink that gives [astMarker] a
// statement to instrument. The boolean is otherwise unused.
var astMarkerCalled bool

// File is the root node - one per `.craftgo` source file.
//
// Decorators are the file-level decorators that appear BEFORE `package` (e.g.
// `@title`, `@version`). Decorators that appear without a `package` keyword
// belong to the first declaration instead and are attached there by the
// parser.
type File struct {
	// LeadingDoc preserves a `//` block at the very top of the file
	// when the first AST-bearing token is a file-level decorator
	// (`@title`, `@version`, ...). [Decorator] has no Doc field, so
	// without LeadingDoc the lexer-attached comment would be lost
	// after the parse / format round trip. When the first token is
	// `package`, the same comment lands on [PackageDecl.Doc] instead
	// and LeadingDoc stays empty.
	LeadingDoc []string
	Decorators []*Decorator
	Package    *PackageDecl
	Imports    []*Import
	Decls      []Decl
}

// PackageDecl is the `package <name>` line. Optional in single-file projects;
// required when more than one file participates in the same logical package.
type PackageDecl struct {
	Pos  Pos
	Doc  []string
	Name string
}

// Import models a single `import [alias] "path"` line. Alias is empty when
// omitted; semantic phase derives a default alias from the last path segment.
type Import struct {
	Pos   Pos
	Alias string
	Path  string
}

// Decl is the interface implemented by every top-level declaration node
// ([TypeDecl], [EnumDecl], [ErrorDecl], [ScalarDecl], [MiddlewareDecl],
// [ServiceDecl]). Use a type switch on Decl to dispatch.
type Decl interface {
	declNode()
	// DeclName returns the declared identifier (used for uniqueness checks).
	DeclName() string
	// DeclPos returns the position of the declaration keyword.
	DeclPos() Pos
}

// TypeDecl is `type Name[<TypeParams>] { Body }`. TypeParams is non-empty
// only for generic declarations (e.g. `Page<T>`); concrete instances are
// represented inline at the call site via [NamedTypeRef.Args].
type TypeDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
	TypeParams []string
	Body       []TypeMember
}

func (*TypeDecl) declNode()          { astMarker() }
func (d *TypeDecl) DeclName() string { return d.Name }
func (d *TypeDecl) DeclPos() Pos     { return d.Pos }

// TypeMember is the interface for items inside a `{}` type body -
// either a [Field] or a [Mixin].
type TypeMember interface {
	typeMember()
	// MemberPos returns the position of the member's first token.
	MemberPos() Pos
}

// Field is a single `name TypeRef [@decorators]` line in a type body.
// The Decorators slice holds both the leading and trailing decorator chains
// merged in source order (parser-side concatenation).
type Field struct {
	Pos        Pos
	Doc        []string
	Name       string
	Type       *TypeRef
	Decorators []*Decorator
}

func (*Field) typeMember()      { astMarker() }
func (f *Field) MemberPos() Pos { return f.Pos }

// Mixin is a bare reference (qualified ident, optionally generic) inside a
// type body. The semantic phase expands its fields into the host type.
type Mixin struct {
	Pos Pos
	Ref *NamedTypeRef
}

func (*Mixin) typeMember()      { astMarker() }
func (m *Mixin) MemberPos() Pos { return m.Pos }

// EnumDecl is `enum Name { Values* }`. All values must be of the same kind
// (all bare, all int, or all string); semantic phase enforces that.
type EnumDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
	Values     []*EnumValue
}

func (*EnumDecl) declNode()          { astMarker() }
func (d *EnumDecl) DeclName() string { return d.Name }
func (d *EnumDecl) DeclPos() Pos     { return d.Pos }

// EnumValueKind tags the runtime representation of an enum value.
type EnumValueKind int

const (
	// EnumBare - `Active` (no `=`); rendered as a Go string constant whose
	// value matches the identifier.
	EnumBare EnumValueKind = iota
	// EnumInt - `Active = 1`; rendered as an `int` constant.
	EnumInt
	// EnumString - `Active = "active"`; rendered as a `string` constant.
	EnumString
)

// EnumValue is one entry inside an enum declaration. IntValue / StrValue are
// only meaningful when Kind matches.
type EnumValue struct {
	Pos        Pos
	Name       string
	Kind       EnumValueKind
	IntValue   int64
	StrValue   string
	Decorators []*Decorator
}

// ErrorDecl is `error <Category> Name [{ Body }]`. Body is optional - the
// shortest form (`error NotFound UserNotFound`) inherits all defaults from
// the category. HasBody distinguishes "explicit empty body `{}`" from "no
// body at all" (both produce empty Body slice).
type ErrorDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Category   string
	Name       string
	Body       []TypeMember
	HasBody    bool
}

func (*ErrorDecl) declNode()          { astMarker() }
func (d *ErrorDecl) DeclName() string { return d.Name }
func (d *ErrorDecl) DeclPos() Pos     { return d.Pos }

// ScalarDecl is `scalar Name <PrimitiveType> [@decorators]`. Doc holds
// the run of `//` comments immediately preceding the `scalar` keyword,
// captured for hover popups and round-trip-safe formatting.
type ScalarDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
	Primitive  string
}

func (*ScalarDecl) declNode()          { astMarker() }
func (d *ScalarDecl) DeclName() string { return d.Name }
func (d *ScalarDecl) DeclPos() Pos     { return d.Pos }

// MiddlewareDecl is `middleware Name [(Params)]`. Params is non-nil only
// when parentheses are present; an empty parameter list `()` and no
// parentheses both produce nil. Doc preserves the leading `//` block.
type MiddlewareDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
	Params     []*MiddlewareParam
}

func (*MiddlewareDecl) declNode()          { astMarker() }
func (d *MiddlewareDecl) DeclName() string { return d.Name }
func (d *MiddlewareDecl) DeclPos() Pos     { return d.Pos }

// MiddlewareParam is one entry in `middleware Name(p1: T1 = default, ...)`.
// Default may be nil when no `= literal` follows.
type MiddlewareParam struct {
	Pos     Pos
	Name    string
	Type    *TypeRef
	Default Expr
}

// ServiceDecl is either a primary `service Name { ... }` (Extend == false) or
// a continuation `extend service Name { ... }` (Extend == true). The
// semantic phase merges all extends into the primary.
type ServiceDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
	Methods    []*Method
	Extend     bool
}

func (*ServiceDecl) declNode()          { astMarker() }
func (d *ServiceDecl) DeclName() string { return d.Name }
func (d *ServiceDecl) DeclPos() Pos     { return d.Pos }

// Method is a single `<verb> Name [path] { request? response? }`. Path is nil
// when the method body had no leading `/segment` - the runtime listens at
// `basePath + servicePrefix` in that case.
type Method struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Verb       string
	Name       string
	Path       *Path
	Request    *NamedTypeRef
	Response   *MethodResponse
}

// MethodResponse describes the response side of a method. The framework
// always JSON-encodes the named type; endpoints that want to bypass the
// framework entirely use the `@passthrough` decorator (which forbids a
// response block) instead.
type MethodResponse struct {
	Pos  Pos
	Type *NamedTypeRef
}

// Path is the parsed representation of a route path. Each segment is either
// a literal (possibly hyphenated like `api-v1`) or a `{param}`.
type Path struct {
	Pos      Pos
	Segments []*PathSegment
}

// PathSegment models one `/segment` between slashes. Param == true means the
// source had `{Literal}`; otherwise Literal is the literal text. An empty
// Literal with Param == false represents a trailing slash.
type PathSegment struct {
	Pos     Pos
	Param   bool
	Literal string
}

// Decorator is `@name[(args...)]`. Args is non-nil only when parentheses
// are present (so `@deprecated` differs from `@deprecated()` - both produce
// nil Args, distinguishable only by source if needed).
type Decorator struct {
	Pos  Pos
	Name string
	Args []*DecoratorArg
}

// DecoratorArg is one argument inside `@name(...)`. Exactly one of Value,
// Nested, or Object is populated:
//
//   - Value (and optional Name+Named=true) for bare or `name: value` literals;
//   - Nested for `@inner(...)` arguments such as `@each(@length(1, 20))`;
//   - Object for `{ key: value, ... }` literals such as `@example({...})`.
type DecoratorArg struct {
	Pos    Pos
	Name   string
	Named  bool
	Value  Expr
	Nested *Decorator
	Object []*ObjectField
}

// ObjectField is one `name: value` pair inside a `{}` decorator argument.
type ObjectField struct {
	Pos   Pos
	Name  string
	Value Expr
}

// Expr is the interface implemented by every literal value node that may
// appear as a decorator argument or default value.
type Expr interface {
	exprNode()
	// ExprPos returns the start position of the expression.
	ExprPos() Pos
}

// StringLit holds the unescaped contents of a `"..."` or backtick literal.
// (The lexer keeps escapes verbatim; the parser unescapes when constructing
// this node.)
type StringLit struct {
	Pos   Pos
	Value string
}

func (*StringLit) exprNode()      { astMarker() }
func (e *StringLit) ExprPos() Pos { return e.Pos }

// IntLit is a parsed signed integer literal.
type IntLit struct {
	Pos   Pos
	Value int64
}

func (*IntLit) exprNode()      { astMarker() }
func (e *IntLit) ExprPos() Pos { return e.Pos }

// FloatLit is a parsed signed float64 literal.
type FloatLit struct {
	Pos   Pos
	Value float64
}

func (*FloatLit) exprNode()      { astMarker() }
func (e *FloatLit) ExprPos() Pos { return e.Pos }

// BoolLit holds `true` or `false`.
type BoolLit struct {
	Pos   Pos
	Value bool
}

func (*BoolLit) exprNode()      { astMarker() }
func (e *BoolLit) ExprPos() Pos { return e.Pos }

// NullLit holds the `null` keyword.
type NullLit struct {
	Pos Pos
}

func (*NullLit) exprNode()      { astMarker() }
func (e *NullLit) ExprPos() Pos { return e.Pos }

// DurationLit keeps the original source text (e.g. "5s", "1.5ms") rather
// than a parsed time.Duration so the formatter can round-trip exactly and
// the runtime can choose its own resolution.
type DurationLit struct {
	Pos  Pos
	Text string
}

func (*DurationLit) exprNode()      { astMarker() }
func (e *DurationLit) ExprPos() Pos { return e.Pos }

// SizeLit keeps the original source text (e.g. "1MB"). Same rationale as
// [DurationLit].
type SizeLit struct {
	Pos  Pos
	Text string
}

func (*SizeLit) exprNode()      { astMarker() }
func (e *SizeLit) ExprPos() Pos { return e.Pos }

// IdentExpr is a reference to a named value (an enum value, a middleware
// name, etc.) used inside decorator arguments. Resolution happens in the
// semantic phase.
type IdentExpr struct {
	Pos  Pos
	Name *QualifiedIdent
}

func (*IdentExpr) exprNode()      { astMarker() }
func (e *IdentExpr) ExprPos() Pos { return e.Pos }

// ArrayLit is a `[v1, v2, ...]` literal. Elements may be of mixed kind so
// the runtime / codegen handles validation per decorator.
type ArrayLit struct {
	Pos      Pos
	Elements []Expr
}

func (*ArrayLit) exprNode()      { astMarker() }
func (e *ArrayLit) ExprPos() Pos { return e.Pos }

// QualifiedIdent is `pkg.Name` (or just `Name`). Parts is non-empty; for an
// unqualified name it has length 1.
type QualifiedIdent struct {
	Pos   Pos
	Parts []string
}

// String returns the dotted form, e.g. `pkg.Name` or `Name`.
func (q *QualifiedIdent) String() string { return strings.Join(q.Parts, ".") }

// TypeRef describes a type expression. Exactly one of Map or Named is set;
// Array and Optional are independent suffix flags so `T[]?` is legal.
//
// `ArrayDepth` is the number of trailing `[]` suffixes parsed (0 =
// not an array). The legacy `Array bool` is kept as a derived
// convenience for the wide set of call sites that only care
// whether the field is "any kind of array" - it equals
// `ArrayDepth > 0` after every parse.
type TypeRef struct {
	Pos      Pos
	Map      *MapType
	Named    *NamedTypeRef
	Array    bool
	Optional bool
	// ArrayDepth captures multi-dimensional arrays (`Tag[][]` →
	// 2). Single-dim arrays use depth 1. Code that only needs
	// "is this an array?" can keep checking [Array] / `> 0`
	// equivalently.
	ArrayDepth int
}

// MapType represents `map<K, V>`. Both Key and Value are recursive [TypeRef]
// values so that nested maps and generic instances work uniformly.
type MapType struct {
	Pos   Pos
	Key   *TypeRef
	Value *TypeRef
}

// NamedTypeRef references a declared type, possibly with generic arguments.
// Args is non-empty only for generic instances; the codegen renames such
// instances to e.g. `FooOfUserAndOrg`.
type NamedTypeRef struct {
	Pos  Pos
	Name *QualifiedIdent
	Args []*TypeRef
}
