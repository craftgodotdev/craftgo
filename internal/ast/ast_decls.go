package ast

// TypeDecl is `type Name { ... }`, or `type Name<T, ...> { ... }` for a
// generic; TrailingDoc is the comment after its closing brace.
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

// TypeMember is a [Field], [Mixin] or [FreeComment] in a type body.
type TypeMember interface {
	typeMember()
	// MemberPos returns the position of the member's first token.
	MemberPos() Pos
}

// Field is `name Type` in a type body. Decorators holds the chains before and
// after it, in source order.
type Field struct {
	Pos        Pos
	Doc        []string
	Name       string
	Type       *TypeRef
	Decorators []*Decorator
}

func (*Field) typeMember()      { astMarker() }
func (f *Field) MemberPos() Pos { return f.Pos }

// Mixin is a type named alone in a type body; the host type gains its fields.
type Mixin struct {
	Pos Pos
	Doc []string
	Ref *NamedTypeRef
}

func (*Mixin) typeMember()      { astMarker() }
func (m *Mixin) MemberPos() Pos { return m.Pos }

// FreeComment is a comment block that belongs to no node, in a body or at file
// scope. Pos is its first line; Text has one entry per line.
type FreeComment struct {
	Pos  Pos
	Text []string
}

func (*FreeComment) typeMember()      { astMarker() }
func (*FreeComment) enumMember()      { astMarker() }
func (*FreeComment) serviceMember()   { astMarker() }
func (c *FreeComment) MemberPos() Pos { return c.Pos }

// EnumDecl is `enum Name { ... }`; Members holds [EnumValue] and [FreeComment]
// entries in source order.
type EnumDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Name        string
	Members     []EnumMember
	TrailingDoc []string // comment after the closing brace
}

// EnumValues returns the [EnumValue] members in source order.
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

// EnumMember is an [EnumValue] or a [FreeComment].
type EnumMember interface {
	enumMember()
	// MemberPos returns the position of the member's first token.
	MemberPos() Pos
}

func (*EnumDecl) declNode()          { astMarker() }
func (d *EnumDecl) DeclName() string { return d.Name }
func (d *EnumDecl) DeclPos() Pos     { return d.Pos }

// EnumValueKind is how an enum value is written.
type EnumValueKind int

const (
	// EnumBare is `Active`.
	EnumBare EnumValueKind = iota
	// EnumInt is `Active = 1`.
	EnumInt
	// EnumString is `Active = "active"`.
	EnumString
)

// EnumValue is one enum entry; IntValue and StrValue apply only to the
// matching Kind. StrText is StrValue as written, quotes included.
type EnumValue struct {
	Pos        Pos
	Doc        []string
	Name       string
	Kind       EnumValueKind
	IntValue   int64
	StrValue   string
	StrText    string
	Decorators []*Decorator
}

func (*EnumValue) enumMember()      { astMarker() }
func (v *EnumValue) MemberPos() Pos { return v.Pos }

// ErrorDecl is `error Category Name` with an optional `{ ... }` body; HasBody
// tells an empty `{}` from no body.
type ErrorDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Category    string
	Name        string
	Body        []TypeMember
	HasBody     bool
	TrailingDoc []string // comment after the closing brace
}

func (*ErrorDecl) declNode()          { astMarker() }
func (d *ErrorDecl) DeclName() string { return d.Name }
func (d *ErrorDecl) DeclPos() Pos     { return d.Pos }

// ScalarDecl is `scalar Name primitive`.
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

// MiddlewareDecl is `middleware Name`.
type MiddlewareDecl struct {
	Pos        Pos
	Decorators []*Decorator
	Doc        []string
	Name       string
}

func (*MiddlewareDecl) declNode()          { astMarker() }
func (d *MiddlewareDecl) DeclName() string { return d.Name }
func (d *MiddlewareDecl) DeclPos() Pos     { return d.Pos }

// ServiceDecl is `service Name { ... }`, or `extend service Name { ... }` when
// Extend is set. Members holds [Method] and [FreeComment] entries in order.
type ServiceDecl struct {
	Pos         Pos
	Decorators  []*Decorator
	Doc         []string
	Name        string
	Members     []ServiceMember
	Extend      bool
	TrailingDoc []string // comment after the closing brace
}

func (*ServiceDecl) declNode()          { astMarker() }
func (d *ServiceDecl) DeclName() string { return d.Name }
func (d *ServiceDecl) DeclPos() Pos     { return d.Pos }

// Methods returns the [Method] members in source order.
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

// ServiceMember is a [Method] or a [FreeComment].
type ServiceMember interface {
	serviceMember()
	// MemberPos returns the position of the member's first token.
	MemberPos() Pos
}

// Method is `verb Name /path { ... }`; Path is nil when omitted.
type Method struct {
	Pos          Pos
	Decorators   []*Decorator
	Doc          []string
	Verb         string
	Name         string
	Path         *Path
	Request      *NamedTypeRef
	Response     *MethodResponse
	TrailingDoc  []string       // comment after the closing brace
	BodyComments []*FreeComment // comment blocks inside the body
	EndPos       Pos            // the closing brace
}

func (*Method) serviceMember()   { astMarker() }
func (m *Method) MemberPos() Pos { return m.Pos }

// MethodResponse is a method's `response Type` clause.
type MethodResponse struct {
	Pos  Pos
	Type *NamedTypeRef
}

// EventDecl is `event Name { payload Type }`.
type EventDecl struct {
	Pos          Pos
	Decorators   []*Decorator
	Doc          []string
	Name         string
	Payload      *EventPayload
	TrailingDoc  []string       // comment after the closing brace
	BodyComments []*FreeComment // comment blocks inside the body
	EndPos       Pos            // the closing brace
}

func (*EventDecl) declNode()          { astMarker() }
func (e *EventDecl) DeclName() string { return e.Name }
func (e *EventDecl) DeclPos() Pos     { return e.Pos }

// EventPayload is the `payload Type` clause of an event.
type EventPayload struct {
	Pos   Pos
	Type  *NamedTypeRef
	Array bool // `payload Type[]`
}

// Path is a method's route, such as `/users/{id}`.
type Path struct {
	Pos      Pos
	Segments []*PathSegment
}

// PathSegment is one segment of a [Path]: `{Literal}` when Param is set,
// otherwise literal text. The root path `/` is one empty segment.
type PathSegment struct {
	Pos     Pos
	Param   bool
	Literal string
}
