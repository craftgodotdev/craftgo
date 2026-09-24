package format

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (p *Printer) Decorator(d *ast.Decorator) {
	p.decoratorCore(d)
	if d.TrailingDoc != "" {
		p.write("  // ")
		p.write(d.TrailingDoc)
	}
}

// decoratorCore writes `@name(args)` without d's trailing comment.
func (p *Printer) decoratorCore(d *ast.Decorator) {
	p.write("@")
	name := d.Name
	p.write(name)
	if len(d.Args) > 0 {
		p.write("(")
		for i, a := range d.Args {
			if i > 0 {
				p.write(", ")
			}
			p.decoratorArgInContext(name, i, a)
		}
		p.write(")")
	}
}

// decoratorArgInContext prints argument idx of decoratorName; a first positional
// @format string that is an identifier prints bare (`@format(email)`).
func (p *Printer) decoratorArgInContext(decoratorName string, idx int, a *ast.DecoratorArg) {
	if decoratorName == "format" && idx == 0 && !a.Named {
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
		p.write("{")
		for i, f := range a.Object {
			if i > 0 {
				p.write(", ")
			}
			p.write(f.Name)
			p.write(": ")
			p.Expr(f.Value)
		}
		p.write("}")
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
		p.write("[")
		for i, el := range v.Elements {
			if i > 0 {
				p.write(", ")
			}
			p.Expr(el)
		}
		p.write("]")
	}
}
