package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

// lengthCheck renders @length(n) or @length(min, max) on a string or bytes field.
func lengthCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
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
	// An init statement cannot follow the nil guard, so the guarded form counts twice.
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

// minMaxLengthCheck renders @minLength or @maxLength (kind "min" or "max") on a
// string or bytes field.
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

// lengthCount measures a string in runes, as OpenAPI minLength/maxLength do,
// and a bytes field in bytes.
func lengthCount(f *ast.Field, val string, ctx emitCtx) string {
	if f != nil && f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "bytes" {
		return "len(" + val + ")"
	}
	ctx.uses["unicode/utf8"] = true
	return "utf8.RuneCountInString(" + val + ")"
}

// patternCheck renders @pattern on a string field against a package-level regex.
func patternCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	s, ok := ast.TextValue(d.Args[0].Value)
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

// formatCheck renders @format on a string field from its [strfmt] entry, a regex
// or a stdlib condition; an unknown format renders nothing.
func formatCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	name, _ := ast.TextValue(d.Args[0].Value)
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
		// Nested: a format condition may carry an init statement, which `&&` cannot guard.
		return fmt.Sprintf("if %s != nil {\n\t%s\n}", access, indentBlock(check))
	}
	return check
}
