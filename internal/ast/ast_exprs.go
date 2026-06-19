// AST: expression / literal nodes + Decorator / DecoratorArg / ObjectField.
package ast

type Decorator struct {
	Pos         Pos
	Name        string
	Args        []*DecoratorArg
	TrailingDoc string
	// HasParens reports whether the source wrote `(...)` after the
	// decorator name, even when the inside was empty. The parser sets
	// it so the semantic analyzer can flag `@positive()` (empty parens
	// on a Flag decorator) and the formatter can normalise it away.
	HasParens bool
	// Propagated marks decorators that the semantic phase copied onto
	// this site from an enclosing scope - currently used by
	// `mergeServices` to flag method-level decorators it cloned from an
	// `extend service` block. Codegen uses the flag to distinguish
	// "inherited" decorators (which `@ignoreMiddleware` / `@ignoreTags`
	// / `@ignoreSecurity` should clear) from the method's own
	// decorators (which they should not).
	Propagated bool
}

// DecoratorArg is one argument inside `@name(...)`. Exactly one of Value,
// Nested, or Object is populated:
//
//   - Value (and optional Name+Named=true) for bare or `name: value` literals;
//   - Nested for `@inner(...)` arguments - the parser preserves the
//     nested-decorator shape so future meta-decorators that consume
//     another decorator can land without grammar churn;
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
