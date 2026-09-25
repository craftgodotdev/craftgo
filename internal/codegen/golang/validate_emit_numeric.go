package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// numericBoundCheck renders @gt/@gte/@lt/@lte on a numeric value; op is the
// relation a valid value satisfies, and the emitted condition negates it.
func numericBoundCheck(t checkTarget, d *ast.Decorator, op, label string, ctx emitCtx) string {
	if !prims.IsNumeric(t.prim) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.NumericArg(d.Args[0])
	if !ok {
		return ""
	}
	var flip string
	switch op {
	case ">=":
		flip = "<"
	case ">":
		flip = "<="
	case "<=":
		flip = ">"
	case "<":
		flip = ">="
	}
	cond := fmt.Sprintf("%s%s %s %s", t.guard(), t.val(), flip, n)
	msg := fmt.Sprintf(`"%s%s %s"`, errSubject(t.subject), label, n)
	return ifReturnf(cond, msg, ctx)
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
	val, guard := t.val(), t.guard()
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("%s < %s || %s > %s", val, lo, val, hi)
	} else {
		cond = fmt.Sprintf("%s(%s < %s || %s > %s)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%sout of range [%s, %s]"`, errSubject(t.subject), lo, hi)
	return ifReturnf(cond, msg, ctx)
}

// signCheck renders @positive or @negative (kind) on a numeric value.
func signCheck(t checkTarget, kind string, ctx emitCtx) string {
	if !prims.IsNumeric(t.prim) {
		return ""
	}
	op, label := "<=", "must be positive"
	if kind == "negative" {
		op, label = ">=", "must be negative"
	}
	cond := fmt.Sprintf("%s%s %s 0", t.guard(), t.val(), op)
	msg := fmt.Sprintf(`"%s%s"`, errSubject(t.subject), label)
	return ifReturnf(cond, msg, ctx)
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
	cond := fmt.Sprintf("%s%s%%%s != 0", t.guard(), t.val(), n)
	msg := fmt.Sprintf(`"%smust be a multiple of %s"`, errSubject(t.subject), n)
	return ifReturnf(cond, msg, ctx)
}
