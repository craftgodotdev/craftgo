package ast

// Decorator is `@Name` or `@Name(args)`.
type Decorator struct {
	Pos         Pos
	Name        string
	Args        []*DecoratorArg
	TrailingDoc string // comment after the decorator on its line
	// HasParens is set when the source wrote `(...)`, even empty.
	HasParens bool
	// Propagated marks a copy semantic prepends to a method from its
	// `extend service` block.
	Propagated bool
}

// DecoratorArg is one decorator argument, held in Value, Nested or Object;
// Named marks the `name: value` form.
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

// Expr is a literal or a name in a decorator argument.
type Expr interface {
	exprNode()
	// ExprPos returns the start position of the expression.
	ExprPos() Pos
}

// StringLit is a string literal; Value is unescaped.
type StringLit struct {
	Pos   Pos
	Value string
}

func (*StringLit) exprNode()      { astMarker() }
func (e *StringLit) ExprPos() Pos { return e.Pos }

// IntLit is a signed integer literal.
type IntLit struct {
	Pos   Pos
	Value int64
}

func (*IntLit) exprNode()      { astMarker() }
func (e *IntLit) ExprPos() Pos { return e.Pos }

// FloatLit is a signed float literal.
type FloatLit struct {
	Pos   Pos
	Value float64
}

func (*FloatLit) exprNode()      { astMarker() }
func (e *FloatLit) ExprPos() Pos { return e.Pos }

// BoolLit is `true` or `false`.
type BoolLit struct {
	Pos   Pos
	Value bool
}

func (*BoolLit) exprNode()      { astMarker() }
func (e *BoolLit) ExprPos() Pos { return e.Pos }

// NullLit is `null`.
type NullLit struct {
	Pos Pos
}

func (*NullLit) exprNode()      { astMarker() }
func (e *NullLit) ExprPos() Pos { return e.Pos }

// DurationLit is a duration literal, kept as its source text (`5s`).
type DurationLit struct {
	Pos  Pos
	Text string
}

func (*DurationLit) exprNode()      { astMarker() }
func (e *DurationLit) ExprPos() Pos { return e.Pos }

// SizeLit is a size literal, kept as its source text (`1MB`).
type SizeLit struct {
	Pos  Pos
	Text string
}

func (*SizeLit) exprNode()      { astMarker() }
func (e *SizeLit) ExprPos() Pos { return e.Pos }

// IdentExpr is a name in a decorator argument, such as an enum value or a
// field.
type IdentExpr struct {
	Pos  Pos
	Name *QualifiedIdent
}

func (*IdentExpr) exprNode()      { astMarker() }
func (e *IdentExpr) ExprPos() Pos { return e.Pos }

// ArrayLit is `[a, b, ...]`; its elements may differ in kind.
type ArrayLit struct {
	Pos      Pos
	Elements []Expr
}

func (*ArrayLit) exprNode()      { astMarker() }
func (e *ArrayLit) ExprPos() Pos { return e.Pos }
