package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

// lengthCheck renders @length(n) or @length(min, max) on a string or bytes value.
func lengthCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !t.primIs(prims.String, prims.Bytes) || len(d.Args) == 0 || len(d.Args) > 2 {
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
	guard := t.guard()
	count := lengthCount(t, ctx)
	// An init statement cannot follow the nil guard, so the guarded form counts twice.
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("l := %s; l < %d || l > %d", count, lo, hi)
	} else {
		cond = fmt.Sprintf("%s(%s < %d || %s > %d)", guard, count, lo, count, hi)
	}
	var msg string
	if lo == hi {
		msg = fmt.Sprintf(`"%slength must be %d"`, errSubject(t.subject), lo)
	} else {
		msg = fmt.Sprintf(`"%slength out of range [%d, %d]"`, errSubject(t.subject), lo, hi)
	}
	return ifReturnf(cond, msg, ctx)
}

// minMaxLengthCheck renders @minLength or @maxLength (kind "min" or "max") on a
// string or bytes value.
func minMaxLengthCheck(t checkTarget, d *ast.Decorator, kind string, ctx emitCtx) string {
	if !t.primIs(prims.String, prims.Bytes) || len(d.Args) != 1 {
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
	cond := fmt.Sprintf("%s%s %s %d", t.guard(), lengthCount(t, ctx), op, n)
	msg := fmt.Sprintf(`"%slength %s %d"`, errSubject(t.subject), label, n)
	return ifReturnf(cond, msg, ctx)
}

// lengthCount measures a string in runes, as OpenAPI minLength/maxLength do,
// and a bytes value in bytes.
func lengthCount(t checkTarget, ctx emitCtx) string {
	if t.primIs(prims.Bytes) {
		return "len(" + t.val() + ")"
	}
	ctx.uses["unicode/utf8"] = true
	return "utf8.RuneCountInString(" + t.val() + ")"
}

// patternCheck renders @pattern on a string value against a package-level regex.
func patternCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !t.primIs(prims.String) || len(d.Args) != 1 {
		return ""
	}
	s, ok := ast.TextValue(d.Args[0].Value)
	if !ok {
		return ""
	}
	ctx.uses["regexp"] = true
	patVar := ctx.regexes.intern(s)
	cond := fmt.Sprintf("%s!%s.MatchString(%s)", t.guard(), patVar, t.val())
	msg := fmt.Sprintf(`"%sdoes not match pattern"`, errSubject(t.subject))
	return ifReturnf(cond, msg, ctx)
}

// formatCheck renders @format on a string value from its [strfmt] entry, a regex
// or a stdlib condition; an unknown format renders nothing.
func formatCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !t.primIs(prims.String) || len(d.Args) != 1 {
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
	msg := fmt.Sprintf(`"%snot a valid %s"`, errSubject(t.subject), sp.Label)
	var check string
	if sp.Pattern != "" {
		ctx.uses["regexp"] = true
		check = ifReturnf("!"+ctx.regexes.intern(sp.Pattern)+".MatchString("+t.val()+")", msg, ctx)
	} else {
		check = ifReturnf(fmt.Sprintf(sp.Cond, t.val()), msg, ctx)
	}
	if t.pointer {
		// Nested: a format condition may carry an init statement, which `&&` cannot guard.
		return fmt.Sprintf("if %s != nil {\n%s\n}", t.access, check)
	}
	return check
}
