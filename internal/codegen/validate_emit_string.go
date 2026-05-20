// String validators: @length, @minLength, @maxLength, @pattern, @format dispatcher.
package codegen

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func lengthCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := intArg(d.Args[0])
	hi, ok2 := intArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	// Avoid the `if X != nil && l := len(*X); ...` form - Go forbids
	// `:=` inside an `&&` expression. Inline `len(...)` twice instead;
	// the second call is constant-folded by the compiler when the
	// argument is a simple deref.
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("l := len(%s); l < %d || l > %d", val, lo, hi)
	} else {
		cond = fmt.Sprintf("%s(len(%s) < %d || len(%s) > %d)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%s: length out of range [%d, %d]"`, f.Name, lo, hi)
	return ifReturnf(cond, msg)
}

// minMaxLengthCheck handles `@minLength(n)` and `@maxLength(n)`.
// Optional string fields are handled the same way as `lengthCheck` -
// nil-guard plus pointer deref.
func minMaxLengthCheck(f *ast.Field, access string, d *ast.Decorator, kind string, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		return ""
	}
	op, label := "<", "less than"
	if kind == "max" {
		op, label = ">", "greater than"
	}
	uses["fmt"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%slen(%s) %s %d", guard, val, op, n)
	msg := fmt.Sprintf(`"%s: length %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// patternCheck handles `@pattern("regex")`. The regex is interned in
// the file's [regexRegistry] so the `regexp.MustCompile` call happens
// ONCE at package init — Validate() references the pre-compiled var
// instead of recompiling per call.
func patternCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	s, ok := stringArg(d.Args[0])
	if !ok {
		return ""
	}
	ctx.uses["fmt"] = true
	ctx.uses["regexp"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	patVar := ctx.regexes.intern(s)
	cond := fmt.Sprintf("%s!%s.MatchString(%s)", guard, patVar, val)
	msg := fmt.Sprintf(`"%s: does not match pattern"`, f.Name)
	return ifReturnf(cond, msg)
}

// formatCheck handles `@format(name)` for the catalogue of standard
// formats. Each entry in [formatValidators] declares the Go imports
// needed and the emit shape (regex, single-expression Go check, or
// init-statement check). The argument may be either a quoted string
// (`@format("email")`) or a bare identifier (`@format(email)`) - both
// accepted. Unknown names skip silently; projects can extend with
// `@pattern("...")` for niche cases.
func formatCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	name := stringOrIdentArg(d.Args[0])
	if name == "" {
		return ""
	}
	v, ok := formatValidators[name]
	if !ok {
		return ""
	}
	for _, imp := range v.imports {
		ctx.uses[imp] = true
	}
	ctx.uses["fmt"] = true
	val := stringValueExpr(f, access)
	msg := fmt.Sprintf(`"%s: not a valid %s"`, f.Name, v.label)
	// Regex-backed formats intern their pattern in the package-level
	// registry so `MustCompile` runs once; stdlib-backed formats
	// (mail/url/time/...) emit their init-stmt verbatim.
	emit := v.emit
	if v.pattern != "" {
		patVar := ctx.regexes.intern(v.pattern)
		emit = func(val, msg string) string {
			return ifReturnf("!"+patVar+".MatchString("+val+")", msg)
		}
	}
	if f.Type.Optional {
		// Pointer field: nest the check inside a nil-guard so the
		// init-stmt forms (mail.ParseAddress / time.Parse / ...) only
		// run when a value is present.
		inner := emit(val, msg)
		return fmt.Sprintf("if %s != nil {\n\t%s\n}", access, indentBlock(inner))
	}
	return emit(val, msg)
}
