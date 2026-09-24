package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func numericValueExpr(f *ast.Field, access string, ctx emitCtx) string {
	if goFieldIsPointer(f, ctx.pkg, ctx.resolver) {
		return "*" + access
	}
	return access
}

// numericBoundCheck renders @gt/@gte/@lt/@lte on a numeric field; op is the
// relation a valid value satisfies, and the emitted condition negates it.
func numericBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, ctx emitCtx) string {
	if !isNumericField(f) || len(d.Args) != 1 {
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
	default:
		return ""
	}
	val := numericValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s %s", guard, val, flip, n)
	msg := fmt.Sprintf(`"%s%s %s"`, errSubject(fieldWireName(f)), label, n)
	return ifReturnf(cond, msg, ctx)
}

// rangeCheck renders @range(lo, hi) on a numeric field as one inclusive bound check.
func rangeCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isNumericField(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := semantic.NumericArg(d.Args[0])
	hi, ok2 := semantic.NumericArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	val := numericValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("%s < %s || %s > %s", val, lo, val, hi)
	} else {
		cond = fmt.Sprintf("%s(%s < %s || %s > %s)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%sout of range [%s, %s]"`, errSubject(fieldWireName(f)), lo, hi)
	return ifReturnf(cond, msg, ctx)
}

// signCheck renders @positive or @negative (kind) on a numeric field.
func signCheck(f *ast.Field, access, kind string, ctx emitCtx) string {
	if !isNumericField(f) {
		return ""
	}
	op, label := "<=", "must be positive"
	if kind == "negative" {
		op, label = ">=", "must be negative"
	}
	val := numericValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s 0", guard, val, op)
	msg := fmt.Sprintf(`"%s%s"`, errSubject(fieldWireName(f)), label)
	return ifReturnf(cond, msg, ctx)
}

// multipleOfCheck renders @multipleOf on an integer field.
func multipleOfCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isIntegerField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(d.Args[0])
	if !ok {
		// A whole-valued float literal (`@multipleOf(5.0)`) is an integer divisor.
		if fl, fok := d.Args[0].Value.(*ast.FloatLit); fok && fl.Value == float64(int64(fl.Value)) {
			n, ok = int64(fl.Value), true
		}
	}
	if !ok || n == 0 {
		return ""
	}
	val := numericValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s%%%d != 0", guard, val, n)
	msg := fmt.Sprintf(`"%smust be a multiple of %d"`, errSubject(fieldWireName(f)), n)
	return ifReturnf(cond, msg, ctx)
}
