package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// boundLiteral renders a numeric bound for a value of primitive prim: for an
// integer, the exact whole number it writes (`1e19` as 10000000000000000000),
// for a float its Go float text.
func boundLiteral(a *ast.DecoratorArg, prim string) (string, bool) {
	l, ok := semantic.ParseNumericArg(a)
	if !ok {
		return "", false
	}
	if prims.IsInteger(prim) {
		return l.WholeText()
	}
	return l.Text(), true
}

// valueBoundLabels names what a value on each side of its limit fails.
var valueBoundLabels = map[semantic.BoundSide]string{
	{Lower: true, Strict: true}: "must be greater than",
	{Lower: true}:               "below minimum",
	{Strict: true}:              "must be less than",
	{}:                          "above maximum",
}

// numericBoundCheck renders @gt/@gte/@lt/@lte on a numeric value, failing it
// by its side's comparison with the bound; a bound the type enforces renders
// nothing.
func numericBoundCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if !prims.IsNumeric(t.prim) || len(args) != 1 || semantic.BoundImpliedByType(t.prim, d, 0) {
		return ""
	}
	n, ok := boundLiteral(args[0], t.prim)
	if !ok {
		return ""
	}
	return failIf(t.guarded(t.val()+" "+sides[0].FailOp()+" "+n), t.subject, valueBoundLabels[sides[0]]+" "+n, ctx)
}

// rangeCheck renders @range(lo, hi) on a numeric value as one bound check of
// each end the type does not enforce.
func rangeCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if !prims.IsNumeric(t.prim) || len(args) != 2 {
		return ""
	}
	lo, ok1 := boundLiteral(args[0], t.prim)
	hi, ok2 := boundLiteral(args[1], t.prim)
	if !ok1 || !ok2 {
		return ""
	}
	var fails []string
	for i, n := range []string{lo, hi} {
		if !semantic.BoundImpliedByType(t.prim, d, i) {
			fails = append(fails, t.val()+" "+sides[i].FailOp()+" "+n)
		}
	}
	if len(fails) == 0 {
		return ""
	}
	return failIf(t.guarded(strings.Join(fails, " || ")), t.subject, fmt.Sprintf("out of range [%s, %s]", lo, hi), ctx)
}

// signCheck renders @positive or @negative on a numeric value, failing it by
// its side's comparison with 0.
func signCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	sides, _ := semantic.BoundSides(d.Name)
	if !prims.IsNumeric(t.prim) || len(sides) != 1 {
		return ""
	}
	label := "must be negative"
	if sides[0].Lower {
		label = "must be positive"
	}
	return failIf(t.guarded(t.val()+" "+sides[0].FailOp()+" 0"), t.subject, label, ctx)
}

// multipleOfCheck renders @multipleOf on an integer value; a whole float
// divisor such as 5.0 is the integer it holds.
func multipleOfCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !prims.IsInteger(t.prim) || len(d.Args) != 1 {
		return ""
	}
	l, ok := semantic.ParseNumericArg(d.Args[0])
	if !ok {
		return ""
	}
	n, whole := l.WholeText()
	if !whole || n == "0" {
		return ""
	}
	return failIf(t.guarded(t.val()+"%"+n+" != 0"), t.subject, "must be a multiple of "+n, ctx)
}
