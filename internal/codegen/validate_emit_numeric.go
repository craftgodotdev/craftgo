// Numeric validators: @gt/@gte/@lt/@lte/@range/@positive/@negative/@multipleOf.
package codegen

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func numericValueExpr(f *ast.Field, access string) string {
	if goFieldIsPointer(f) {
		return "*" + access
	}
	return access
}

// numericBoundCheck handles the 4 comparison decorators
// `@gt(n)` / `@gte(n)` / `@lt(n)` / `@lte(n)`. `op` is the
// validity predicate the value must satisfy; the emitted condition is
// the NEGATION (true when invalid).
//
//	@gte(0): valid if x >= 0  → fail if x < 0
//	@gt(0):  valid if x > 0   → fail if x <= 0
//	@lte(N): valid if x <= N  → fail if x > N
//	@lt(N):  valid if x < N   → fail if x >= N
//
// Both int and float bound literals are accepted ([numericArg] handles
// the rendering). Float fields with float bounds (`@gte(0.5)` on
// float64) work the same as int-on-int.
func numericBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := numericArg(d.Args[0])
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
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s %s", guard, val, flip, n)
	msg := fmt.Sprintf(`"%s: %s %s"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// rangeCheck combines @gte and @lte into one bounded comparison.
// Pointer fields (T? / `T @nullable`) get the same nil-guard +
// deref treatment as [numericBoundCheck]. Both int and float bound
// literals accepted.
func rangeCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := numericArg(d.Args[0])
	hi, ok2 := numericArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("%s < %s || %s > %s", val, lo, val, hi)
	} else {
		// Same pattern as the optional-string `lengthCheck`: avoid
		// `init; cond` syntax inside `&&` by inlining the bounds
		// twice. Compiler folds the duplicate deref.
		cond = fmt.Sprintf("%s(%s < %s || %s > %s)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%s: out of range [%s, %s]"`, f.Name, lo, hi)
	return ifReturnf(cond, msg)
}

// signCheck handles `@positive` (value > 0) and `@negative` (value < 0)
// on numeric fields. Both produce a one-line conditional with no decorator
// arguments - unlike `@min` they don't carry a bound, so the helper is a
// pure dispatch on the kind string.
func signCheck(f *ast.Field, access, kind string, uses map[string]bool) string {
	if !isNumericField(f) {
		return ""
	}
	uses["fmt"] = true
	op, label := "<=", "must be positive"
	if kind == "negative" {
		op, label = ">=", "must be negative"
	}
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s 0", guard, val, op)
	msg := fmt.Sprintf(`"%s: %s"`, f.Name, label)
	return ifReturnf(cond, msg)
}

// multipleOfCheck handles `@multipleOf(n)` on integer fields. Floats are
// excluded because `%` is integer-only in Go and a runtime modulus on a
// float is rarely what designers intend (rounding error). A future revision
// can layer a tolerance-based check for floats.
func multipleOfCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isIntegerField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		// Accept a whole-valued float literal (`@multipleOf(5.0)`): the
		// OpenAPI side already emits it, and `%` needs an integer divisor,
		// so the two stages would otherwise disagree (spec advertises it,
		// runtime drops it).
		if fl, fok := d.Args[0].Value.(*ast.FloatLit); fok && fl.Value == float64(int64(fl.Value)) {
			n, ok = int64(fl.Value), true
		}
	}
	if !ok || n == 0 {
		return ""
	}
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s%%%d != 0", guard, val, n)
	msg := fmt.Sprintf(`"%s: must be a multiple of %d"`, f.Name, n)
	return ifReturnf(cond, msg)
}
