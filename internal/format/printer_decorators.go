package format

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
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
// @format string spelled like an identifier prints bare (`@format(email)`).
func (p *Printer) decoratorArgInContext(decoratorName string, idx int, a *ast.DecoratorArg) {
	if decoratorName == "format" && idx == 0 && !a.Named {
		if s, ok := a.Value.(*ast.StringLit); ok && isPlainIdent(s.Value) {
			p.write(s.Value)
			return
		}
	}
	p.DecoratorArg(a)
}

// isPlainIdent reports whether s is spelled like an identifier; keywords such
// as null and true also pass.
func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
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
		p.write(strconv.Quote(v.Value))
	case *ast.IntLit:
		p.write(strconv.FormatInt(v.Value, 10))
	case *ast.FloatLit:
		p.write(strconv.FormatFloat(v.Value, 'g', -1, 64))
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
