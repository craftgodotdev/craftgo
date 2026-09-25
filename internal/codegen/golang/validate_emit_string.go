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
	count := lengthCount(t, ctx)
	if lo == hi {
		return failIf(t.guarded(fmt.Sprintf("%s != %d", count, lo)), t.subject, fmt.Sprintf("length must be %d", lo), ctx)
	}
	text := fmt.Sprintf("length out of range [%d, %d]", lo, hi)
	if !countCanFail("<", lo) {
		return failIf(t.guarded(fmt.Sprintf("%s > %d", count, hi)), t.subject, text, ctx)
	}
	// The init statement counts once for both bounds, so a nil guard wraps it.
	return t.guardBlock(failIf(fmt.Sprintf("l := %s; l < %d || l > %d", count, lo, hi), t.subject, text, ctx))
}

// minMaxLengthCheck renders @minLength or @maxLength on a string or bytes
// value, failing it when `length failOp n` holds.
func minMaxLengthCheck(t checkTarget, d *ast.Decorator, failOp, label string, ctx emitCtx) string {
	if !t.primIs(prims.String, prims.Bytes) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(d.Args[0])
	if !ok || !countCanFail(failOp, n) {
		return ""
	}
	cond := fmt.Sprintf("%s %s %d", lengthCount(t, ctx), failOp, n)
	return failIf(t.guarded(cond), t.subject, fmt.Sprintf("%s %d", label, n), ctx)
}

// countCanFail reports whether a count, a length or a number of items, can
// fail `count failOp n`; no count is below 0.
func countCanFail(failOp string, n int64) bool {
	return failOp != "<" || n > 0
}

// lengthCount measures a string in runes, as OpenAPI minLength/maxLength do,
// and a bytes value in bytes.
func lengthCount(t checkTarget, ctx emitCtx) string {
	if t.primIs(prims.Bytes) {
		return "len(" + t.val() + ")"
	}
	ctx.imports.use("unicode/utf8")
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
	ctx.imports.use("regexp")
	cond := "!" + ctx.regexes.intern(s) + ".MatchString(" + t.val() + ")"
	return failIf(t.guarded(cond), t.subject, "does not match pattern", ctx)
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
		ctx.imports.use(imp)
	}
	var cond string
	if sp.Pattern != "" {
		ctx.imports.use("regexp")
		cond = "!" + ctx.regexes.intern(sp.Pattern) + ".MatchString(" + t.val() + ")"
	} else {
		cond = fmt.Sprintf(sp.Cond, t.val())
	}
	// A format condition may carry an init statement, which only a block can guard.
	return t.guardBlock(failIf(cond, t.subject, "not a valid "+sp.Label, ctx))
}
