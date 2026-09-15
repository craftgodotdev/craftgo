// String validators: @length, @minLength, @maxLength, @pattern, @format dispatcher.
package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

func lengthCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	// `@length(N)` is the exact-length form (min == max == N); the
	// two-arg `@length(min, max)` is a range. Both lower to one len()
	// bounds check.
	if !isLengthCheckable(f) || len(d.Args) == 0 || len(d.Args) > 2 {
		return ""
	}
	lo, ok1 := semantic.IntArg(d.Args[0])
	if !ok1 {
		return ""
	}
	hi := lo
	if len(d.Args) == 2 {
		v, ok2 := semantic.IntArg(d.Args[1])
		if !ok2 {
			return ""
		}
		hi = v
	}
	val := stringValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	count := lengthCount(f, val, ctx)
	// Avoid the `if X != nil && l := count(*X); ...` form - Go forbids
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
		msg = fmt.Sprintf(`"%slength must be %d"`, errSubject(fieldWireName(f)), lo)
	} else {
		msg = fmt.Sprintf(`"%slength out of range [%d, %d]"`, errSubject(fieldWireName(f)), lo, hi)
	}
	return ifReturnf(cond, msg, ctx)
}

// minMaxLengthCheck handles `@minLength(n)` and `@maxLength(n)`.
// Optional string fields are handled the same way as `lengthCheck` -
// nil-guard plus pointer deref.
func minMaxLengthCheck(f *ast.Field, access string, d *ast.Decorator, kind string, ctx emitCtx) string {
	if !isLengthCheckable(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(d.Args[0])
	if !ok {
		return ""
	}
	op, label := "<", "less than"
	if kind == "max" {
		op, label = ">", "greater than"
	}
	val := stringValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s %d", guard, lengthCount(f, val, ctx), op, n)
	msg := fmt.Sprintf(`"%slength %s %d"`, errSubject(fieldWireName(f)), label, n)
	return ifReturnf(cond, msg, ctx)
}

// lengthCount returns the Go expression for the length a string-family field's
// `@length` / `@minLength` / `@maxLength` validates: utf8.RuneCountInString for
// a `string` so the bound counts Unicode characters - matching the OpenAPI
// `minLength`/`maxLength` keyword and a Postgres `varchar(n)`, both of which
// count characters, not bytes. A `bytes` field keeps `len()` (raw byte count,
// the right measure for binary, and not advertised in the OpenAPI schema).
func lengthCount(f *ast.Field, val string, ctx emitCtx) string {
	if f != nil && f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "bytes" {
		return "len(" + val + ")"
	}
	ctx.uses["unicode/utf8"] = true
	return "utf8.RuneCountInString(" + val + ")"
}

// patternCheck handles `@pattern("regex")`. The regex is interned in
// the file's [regexRegistry] so the `regexp.MustCompile` call happens
// ONCE at package init - Validate() references the pre-compiled var
// instead of recompiling per call.
func patternCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	s, ok := semantic.StringArg(d.Args[0])
	if !ok {
		return ""
	}
	ctx.uses["regexp"] = true
	val := stringValueExpr(f, access, ctx)
	guard := optionalGuard(f, access)
	patVar := ctx.regexes.intern(s)
	cond := fmt.Sprintf("%s!%s.MatchString(%s)", guard, patVar, val)
	msg := fmt.Sprintf(`"%sdoes not match pattern"`, errSubject(fieldWireName(f)))
	return ifReturnf(cond, msg, ctx)
}

// formatCheck handles `@format(name)` for the [strfmt] catalogue: each
// spec declares the Go imports its check needs and the check itself - a
// regular expression interned once per file so `MustCompile` runs once,
// or a stdlib-backed condition (mail / url / time / ...) emitted verbatim.
// The argument may be either a quoted string (`@format("email")`) or a
// bare identifier (`@format(email)`) - both accepted. Unknown names skip
// silently; projects can extend with `@pattern("...")` for niche cases.
func formatCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	name := semantic.StringOrIdentArg(d.Args[0])
	if name == "" {
		return ""
	}
	sp, ok := strfmt.Lookup(name)
	if !ok {
		return ""
	}
	for _, imp := range sp.Imports {
		ctx.uses[imp] = true
	}
	val := stringValueExpr(f, access, ctx)
	msg := fmt.Sprintf(`"%snot a valid %s"`, errSubject(fieldWireName(f)), sp.Label)
	var check string
	if sp.Pattern != "" {
		ctx.uses["regexp"] = true
		check = ifReturnf("!"+ctx.regexes.intern(sp.Pattern)+".MatchString("+val+")", msg, ctx)
	} else {
		check = ifReturnf(fmt.Sprintf(sp.Cond, val), msg, ctx)
	}
	if goFieldIsPointer(f, ctx.pkg, ctx.resolver) {
		// Pointer field (`?` optional OR `@nullable`): nest the check
		// inside a nil-guard so the deref in `val` and the init-stmt forms
		// (mail.ParseAddress / time.Parse / ...) only run when a value is
		// present. Keying on Optional alone would miss `@nullable`-without-
		// `?`, which is still a `*string` - an unguarded deref panics on
		// `{"field": null}`.
		return fmt.Sprintf("if %s != nil {\n\t%s\n}", access, indentBlock(check))
	}
	return check
}
