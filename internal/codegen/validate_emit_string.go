// String validators: @length, @minLength, @maxLength, @pattern, @format dispatcher.
package codegen

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func lengthCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	// `@length(N)` is the exact-length form (min == max == N); the
	// two-arg `@length(min, max)` is a range. Both lower to one len()
	// bounds check.
	if !isLengthCheckable(f) || len(d.Args) == 0 || len(d.Args) > 2 {
		return ""
	}
	lo, ok1 := intArg(d.Args[0])
	if !ok1 {
		return ""
	}
	hi := lo
	if len(d.Args) == 2 {
		v, ok2 := intArg(d.Args[1])
		if !ok2 {
			return ""
		}
		hi = v
	}
	uses["fmt"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	count := lengthCount(f, val, uses)
	// Avoid the `if X != nil && l := count(*X); ...` form — Go forbids
	// `:=` inside an `&&` expression. Inline the count twice instead; the
	// second call is constant-folded by the compiler when the argument is a
	// simple deref.
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("l := %s; l < %d || l > %d", count, lo, hi)
	} else {
		cond = fmt.Sprintf("%s(%s < %d || %s > %d)", guard, count, lo, count, hi)
	}
	var msg string
	if lo == hi {
		msg = fmt.Sprintf(`"%s: length must be %d"`, f.Name, lo)
	} else {
		msg = fmt.Sprintf(`"%s: length out of range [%d, %d]"`, f.Name, lo, hi)
	}
	return ifReturnf(cond, msg)
}

// minMaxLengthCheck handles `@minLength(n)` and `@maxLength(n)`.
// Optional string fields are handled the same way as `lengthCheck` -
// nil-guard plus pointer deref.
func minMaxLengthCheck(f *ast.Field, access string, d *ast.Decorator, kind string, uses map[string]bool) string {
	if !isLengthCheckable(f) || len(d.Args) != 1 {
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
	cond := fmt.Sprintf("%s%s %s %d", guard, lengthCount(f, val, uses), op, n)
	msg := fmt.Sprintf(`"%s: length %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// lengthCount returns the Go expression for the length a string-family field's
// `@length` / `@minLength` / `@maxLength` validates: utf8.RuneCountInString for
// a `string` so the bound counts Unicode characters — matching the OpenAPI
// `minLength`/`maxLength` keyword and a Postgres `varchar(n)`, both of which
// count characters, not bytes. A `bytes` field keeps `len()` (raw byte count,
// the right measure for binary, and not advertised in the OpenAPI schema).
func lengthCount(f *ast.Field, val string, uses map[string]bool) string {
	if f != nil && f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "bytes" {
		return "len(" + val + ")"
	}
	uses["unicode/utf8"] = true
	return "utf8.RuneCountInString(" + val + ")"
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
	if goFieldIsPointer(f, ctx.pkg, ctx.resolver) {
		// Pointer field (`?` optional OR `@nullable`): nest the check
		// inside a nil-guard so the deref in `val` and the init-stmt forms
		// (mail.ParseAddress / time.Parse / ...) only run when a value is
		// present. Keying on Optional alone would miss `@nullable`-without-
		// `?`, which is still a `*string` — an unguarded deref panics on
		// `{"field": null}`.
		inner := emit(val, msg)
		return fmt.Sprintf("if %s != nil {\n\t%s\n}", access, indentBlock(inner))
	}
	return emit(val, msg)
}
