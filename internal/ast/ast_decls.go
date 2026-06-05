// AST: top-level declaration types (Type / Enum / Error / Scalar / Middleware / Service) + method / path / member shapes.
package ast

type TypeDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Name        string
	TypeParams  []string
	Body        []TypeMember
	TrailingDoc []string
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

// FreeComment is a free-floating `//` comment block that appears inside a
// type / enum / service body and does not attach to any field, value, or
// method. Common patterns:
//
//   - Section dividers immediately after the opening `{`.
//   - Closing notes (TODO / NOTE) immediately before the `}`.
//   - Stand-alone blocks separated from surrounding members by a blank line.
//
// Text holds one entry per `//` source line, with the leading `// ` (slashes
// plus optional single space) already stripped — the parser populates this
// from the lexer's Doc-attached buffer when the buffer is decided to be
// "free-floating" rather than the next member's leading doc.
//
// Implements [TypeMember], [EnumMember], and [ServiceMember] so the same
// node can sit inside any body kind.
type FreeComment struct {
	Pos  Pos
	Text []string
}

func (*FreeComment) typeMember()      { astMarker() }
func (*FreeComment) enumMember()      { astMarker() }
func (*FreeComment) serviceMember()   { astMarker() }
func (c *FreeComment) MemberPos() Pos { return c.Pos }

// EnumDecl is `enum Name { Members* }`. Members are a mix of [EnumValue] (the
// actual enum entries) and [FreeComment] (free-floating section dividers /
// closing notes). All [EnumValue] entries must share a kind (all bare, all
// int, or all string); semantic phase enforces that. Use [EnumDecl.EnumValues]
// to iterate only the typed values when free-floating comments are not relevant.
type EnumDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Name        string
	Members     []EnumMember
	TrailingDoc []string // `// note` on the same line as the body's closing `}`
}

// EnumValues returns only the [*EnumValue] entries from Members, preserving
// source order. Convenience for callers (semantic, codegen) that want the
// typed list and treat free-floating comments as cosmetic.
func (d *EnumDecl) EnumValues() []*EnumValue {
	if d == nil {
		return nil
	}
	out := make([]*EnumValue, 0, len(d.Members))
	for _, m := range d.Members {
		if v, ok := m.(*EnumValue); ok {
			out = append(out, v)
		}
	}
	return out
}

// EnumMember is the interface implemented by anything that can appear inside
// an `enum` body: [*EnumValue] for typed entries, [*FreeComment] for
// free-floating notes / section dividers.
type EnumMember interface {
	enumMember()
	// MemberPos returns the position of the member's first token.
	MemberPos() Pos
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
	Doc        []string
	Name       string
	Kind       EnumValueKind
	IntValue   int64
	StrValue   string
	Decorators []*Decorator
}

func (*EnumValue) enumMember()      { astMarker() }
func (v *EnumValue) MemberPos() Pos { return v.Pos }

// ErrorDecl is `error <Category> Name [{ Body }]`. Body is optional - the
// shortest form (`error NotFound UserNotFound`) inherits all defaults from
// the category. HasBody distinguishes "explicit empty body `{}`" from "no
// body at all" (both produce empty Body slice).
type ErrorDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Category    string
	Name        string
	Body        []TypeMember
	HasBody     bool
	TrailingDoc []string // `// note` on the same line as the body's closing `}`
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

// MiddlewareDecl is `middleware Name`. The DSL captures only the name -
// configuration (parameter shape, defaults, behaviour) lives in the
// hand-written Go impl file the scaffolder produces. Doc preserves the
// leading `//` block.
type MiddlewareDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
}

func (*MiddlewareDecl) declNode()          { astMarker() }
func (d *MiddlewareDecl) DeclName() string { return d.Name }
func (d *MiddlewareDecl) DeclPos() Pos     { return d.Pos }

// ServiceDecl is either a primary `service Name { ... }` (Extend == false) or
// a continuation `extend service Name { ... }` (Extend == true). The
// semantic phase merges all extends into the primary.
//
// Members is a heterogeneous list of [*Method] (the actual endpoints) and
// [*FreeComment] (free-floating section dividers / closing notes). Use
// [ServiceDecl.Methods] when only the typed endpoints are needed.
type ServiceDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Name        string
	Members     []ServiceMember
	Extend      bool
	TrailingDoc []string // `// note` on the same line as the body's closing `}`
}

func (*ServiceDecl) declNode()          { astMarker() }
func (d *ServiceDecl) DeclName() string { return d.Name }
func (d *ServiceDecl) DeclPos() Pos     { return d.Pos }

// Methods returns only the [*Method] entries from Members in source order.
// Convenience for callers (semantic, codegen) that ignore free-floating
// comments and want the typed list.
func (d *ServiceDecl) Methods() []*Method {
	if d == nil {
		return nil
	}
	out := make([]*Method, 0, len(d.Members))
	for _, m := range d.Members {
		if mm, ok := m.(*Method); ok {
			out = append(out, mm)
		}
	}
	return out
}

// ServiceMember is the interface implemented by anything that can appear inside
// a `service` body: [*Method] for typed endpoints, [*FreeComment] for
// free-floating notes / section dividers.
type ServiceMember interface {
	serviceMember()
	// MemberPos returns the position of the member's first token.
	MemberPos() Pos
}

// Method is a single `<verb> Name [path] { request? response? }`. Path is nil
// when the method body had no leading `/segment` - the runtime listens at
// `basePath + servicePrefix` in that case.
//
// TrailingDoc captures a `// note` on the same line as the closing `}` of
// the method body, e.g. `} // returns 404 if not found`.
type Method struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Verb        string
	Name        string
	Path        *Path
	Request     *NamedTypeRef
	Response    *MethodResponse
	TrailingDoc []string
}

func (*Method) serviceMember()   { astMarker() }
func (m *Method) MemberPos() Pos { return m.Pos }

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
