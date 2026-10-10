package format

import (
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Decorator writes `@name`, or `@name(args)` when it has arguments or a
// comment inside its parentheses.
func (p *Printer) Decorator(d *ast.Decorator) {
	p.write("@")
	p.write(d.Name)
	end := p.src.argsCloseLine(d)
	if len(d.Args) == 0 && !p.argCommentBetween(d.Pos.Line, end) {
		return
	}
	items := make([]listItem, len(d.Args))
	for i, a := range d.Args {
		items[i] = listItem{first: a.Pos.Line, last: p.argLastLine(a), print: func() { p.decoratorArgInContext(d.Name, i, a) }}
	}
	p.list(d.Pos.Line, end, "(", items, ")", true)
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
	case a.Object != nil:
		items := make([]listItem, len(a.Object))
		for i, f := range a.Object {
			items[i] = listItem{first: f.Pos.Line, last: p.exprLastLine(f.Value), print: func() {
				p.write(f.Name)
				p.write(": ")
				p.Expr(f.Value)
			}}
		}
		p.list(a.Pos.Line, p.src.closeLine(a.Pos), "{", items, "}", true)
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
			items[i] = listItem{first: el.ExprPos().Line, last: p.exprLastLine(el), print: func() { p.Expr(el) }}
		}
		p.list(v.Pos.Line, p.src.closeLine(v.Pos), "[", items, "]", true)
	}
}

// listItem is an element of an argument list, an array or an object: its
// first and last source lines, and how it prints.
type listItem struct {
	first, last int
	print       func()
}

// list writes items between opener, on source line open, and closer, on line
// end: on one line or, when the items continue below line open and a trailing
// comment sits on a line of theirs above line end, or a comment sits on a line
// of its own among them, each source line of items on its own line one level
// deeper under the comments above it, each but the last, unless trail, ending
// in a comma, and the closer on a line of its own. A comment trailing line end
// follows the closer and what comes after it there.
func (p *Printer) list(open, end int, opener string, items []listItem, closer string, trail bool) {
	p.write(opener)
	var lines [][]listItem
	for rest := items; len(rest) > 0; {
		k := 1
		for k < len(rest) && rest[k].first == rest[0].first {
			k++
		}
		lines, rest = append(lines, rest[:k]), rest[k:]
	}
	n := len(items)
	keep := n > 0 && items[n-1].first > open && p.trailingBefore(open, end)
	prev := open
	for _, l := range lines {
		keep = keep || p.argCommentBetween(prev, l[0].first)
		prev = l[len(l)-1].last
	}
	if !keep && !p.argCommentBetween(prev, end) {
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
	prev = open
	for k, l := range lines {
		p.argComments(prev, l[0].first)
		p.line(l[0].first)
		for i, it := range l {
			if i > 0 {
				p.write(", ")
			}
			it.print()
		}
		if trail || k < len(lines)-1 {
			p.write(",")
		}
		p.endCode()
		prev = l[len(l)-1].last
	}
	p.argComments(prev, end)
	p.depth--
	p.endLine(min(prev+1, end))
	p.indent()
	p.write(closer)
}

// TypeRef writes t as [ast.TypeRef.String] spells it, an argument list over
// several lines when a comment sits among its arguments.
func (p *Printer) TypeRef(t *ast.TypeRef) {
	if t.Map != nil {
		p.write("map")
		lt := p.src.after(t.Map.Pos, 1)
		items := []listItem{p.typeItem(t.Map.Key), p.typeItem(t.Map.Value)}
		p.list(lt.Pos.Line, p.src.closeLine(lt.Pos), "<", items, ">", false)
	} else {
		p.NamedTypeRef(t.Named)
	}
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		depth = 1
	}
	p.write(strings.Repeat("[]", depth))
	if t.Optional {
		p.write("?")
	}
}

// NamedTypeRef is [Printer.TypeRef] for a named type.
func (p *Printer) NamedTypeRef(n *ast.NamedTypeRef) {
	if n == nil || n.Name == nil {
		p.write("?")
		return
	}
	p.write(n.Name.String())
	if len(n.Args) == 0 {
		return
	}
	lt := p.src.after(n.Pos, 2*len(n.Name.Parts)-1)
	items := make([]listItem, len(n.Args))
	for i, a := range n.Args {
		items[i] = p.typeItem(a)
	}
	p.list(lt.Pos.Line, p.src.closeLine(lt.Pos), "<", items, ">", true)
}

// typeItem is t as an element of a type argument list.
func (p *Printer) typeItem(t *ast.TypeRef) listItem {
	return listItem{first: t.Pos.Line, last: p.src.typeEnd(t).Pos.Line, print: func() { p.TypeRef(t) }}
}

// argLastLine returns the source line a decorator argument ends on.
func (p *Printer) argLastLine(a *ast.DecoratorArg) int {
	switch {
	case a.Object != nil:
		return p.src.closeLine(a.Pos)
	case a.Value != nil:
		return p.exprLastLine(a.Value)
	}
	return a.Pos.Line
}

// exprLastLine returns the source line e ends on.
func (p *Printer) exprLastLine(e ast.Expr) int {
	switch v := e.(type) {
	case *ast.ArrayLit:
		return p.src.closeLine(v.Pos)
	case *ast.StringLit:
		return v.Pos.Line + lineEnds(v.Text)
	}
	return e.ExprPos().Line
}
