package format

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Decorator writes `@name` or `@name(args)`.
func (p *Printer) Decorator(d *ast.Decorator) {
	p.write("@")
	p.write(d.Name)
	if len(d.Args) == 0 {
		return
	}
	items := make([]listItem, len(d.Args))
	for i, a := range d.Args {
		items[i] = listItem{first: a.Pos.Line, last: argLastLine(a), print: func() { p.decoratorArgInContext(d.Name, i, a) }}
	}
	p.list(d.Pos.Line, "(", items, ")")
}

// decoratorArgInContext prints argument idx of decoratorName; a string that is
// an identifier prints bare as the first positional argument of a decorator
// whose first argument names one of a closed set (`@format(email)`).
func (p *Printer) decoratorArgInContext(decoratorName string, idx int, a *ast.DecoratorArg) {
	if spec, known := semantic.DecoratorSpec(decoratorName); known && len(spec.Args.Enum) > 0 && idx == 0 && !a.Named {
		if s, ok := a.Value.(*ast.StringLit); ok && lexer.IsIdent(s.Value) {
			p.write(s.Value)
			return
		}
	}
	p.DecoratorArg(a)
}

func (p *Printer) DecoratorArg(a *ast.DecoratorArg) {
	if a.Named {
		p.write(a.Name)
		p.write(": ")
	}
	switch {
	case a.Nested != nil:
		p.Decorator(a.Nested)
	case a.Object != nil:
		items := make([]listItem, len(a.Object))
		for i, f := range a.Object {
			items[i] = listItem{first: f.Pos.Line, last: exprLastLine(f.Value), print: func() {
				p.write(f.Name)
				p.write(": ")
				p.Expr(f.Value)
			}}
		}
		p.list(a.Pos.Line, "{", items, "}")
	default:
		p.Expr(a.Value)
	}
}

func (p *Printer) Expr(e ast.Expr) {
	switch v := e.(type) {
	case *ast.StringLit:
		p.write(v.Text)
	case *ast.IntLit:
		p.write(strconv.FormatInt(v.Value, 10))
	case *ast.FloatLit:
		p.write(v.Text)
	case *ast.BoolLit:
		if v.Value {
			p.write("true")
		} else {
			p.write("false")
		}
	case *ast.NullLit:
		p.write("null")
	case *ast.DurationLit:
		p.write(v.Text)
	case *ast.SizeLit:
		p.write(v.Text)
	case *ast.IdentExpr:
		p.write(v.Name.String())
	case *ast.ArrayLit:
		items := make([]listItem, len(v.Elements))
		for i, el := range v.Elements {
			items[i] = listItem{first: el.ExprPos().Line, last: exprLastLine(el), print: func() { p.Expr(el) }}
		}
		p.list(v.Pos.Line, "[", items, "]")
	}
}

// listItem is an element of an argument list, an array or an object: its
// first and last source lines, and how it prints.
type listItem struct {
	first, last int
	print       func()
}

// list writes items between opener, on source line open, and closer: on one
// line or, when the items continue below line open and a trailing comment sits
// on their lines, each source line of items on its own line one level deeper
// and the closer on a line of its own.
func (p *Printer) list(open int, opener string, items []listItem, closer string) {
	p.write(opener)
	n := len(items)
	if n == 0 || items[n-1].first <= open || !p.trailingBefore(open, items[n-1].last+1) {
		for i, it := range items {
			if i > 0 {
				p.write(", ")
			}
			it.print()
		}
		p.write(closer)
		return
	}
	p.endCode()
	p.depth++
	for rest := items; len(rest) > 0; {
		k := 1
		for k < len(rest) && rest[k].first == rest[0].first {
			k++
		}
		p.line(rest[0].first)
		for i, it := range rest[:k] {
			if i > 0 {
				p.write(", ")
			}
			it.print()
		}
		p.write(",")
		p.endCode()
		rest = rest[k:]
	}
	p.depth--
	p.endLine(items[n-1].last + 1)
	p.indent()
	p.write(closer)
}

// argLastLine returns the source line a decorator argument ends on.
func argLastLine(a *ast.DecoratorArg) int {
	switch {
	case a.Nested != nil:
		if n := len(a.Nested.Args); n > 0 {
			return argLastLine(a.Nested.Args[n-1])
		}
	case len(a.Object) > 0:
		return exprLastLine(a.Object[len(a.Object)-1].Value)
	case a.Value != nil:
		return exprLastLine(a.Value)
	}
	return a.Pos.Line
}

// exprLastLine returns the source line e ends on.
func exprLastLine(e ast.Expr) int {
	switch v := e.(type) {
	case *ast.ArrayLit:
		if n := len(v.Elements); n > 0 {
			return exprLastLine(v.Elements[n-1])
		}
	case *ast.StringLit:
		return v.Pos.Line + lineEnds(v.Text)
	}
	return e.ExprPos().Line
}
