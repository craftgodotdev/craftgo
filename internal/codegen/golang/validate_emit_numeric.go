package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// numericBoundCheck renders @gt/@gte/@lt/@lte on a numeric value, failing it
// when `value failOp bound` holds.
func numericBoundCheck(t checkTarget, d *ast.Decorator, failOp, label string, ctx emitCtx) string {
	if !prims.IsNumeric(t.prim) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.NumericArg(d.Args[0])
	if !ok {
		return ""
	}
	return failIf(t.guarded(t.val()+" "+failOp+" "+n), t.subject, label+" "+n, ctx)
}

// rangeCheck renders @range(lo, hi) on a numeric value as one inclusive bound check.
func rangeCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !prims.IsNumeric(t.prim) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := semantic.NumericArg(d.Args[0])
	hi, ok2 := semantic.NumericArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	val := t.val()
	cond := fmt.Sprintf("%s < %s || %s > %s", val, lo, val, hi)
	return failIf(t.guarded(cond), t.subject, fmt.Sprintf("out of range [%s, %s]", lo, hi), ctx)
}

// signCheck renders @positive or @negative on a numeric value, failing it when
// `value failOp 0` holds.
func signCheck(t checkTarget, failOp, label string, ctx emitCtx) string {
	if !prims.IsNumeric(t.prim) {
		return ""
	}
	return failIf(t.guarded(t.val()+" "+failOp+" 0"), t.subject, label, ctx)
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
