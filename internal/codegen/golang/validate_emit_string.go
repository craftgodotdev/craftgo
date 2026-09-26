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
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if !t.primIs(prims.String, prims.Bytes) || len(args) != 2 {
		return ""
	}
	lo, ok1 := semantic.IntArg(args[0])
	hi, ok2 := semantic.IntArg(args[1])
	if !ok1 || !ok2 {
		return ""
	}
	loImplied, hiImplied := semantic.BoundImpliedByType(t.prim, d, 0), semantic.BoundImpliedByType(t.prim, d, 1)
	if loImplied && hiImplied {
		return ""
	}
	count := lengthCount(t, ctx)
	if lo == hi {
		return failIf(t.guarded(fmt.Sprintf("%s != %d", count, lo)), t.subject, fmt.Sprintf("length must be %d", lo), ctx)
	}
	text := fmt.Sprintf("length out of range [%d, %d]", lo, hi)
	loFails, hiFails := fmt.Sprintf("%s %d", sides[0].FailOp(), lo), fmt.Sprintf("%s %d", sides[1].FailOp(), hi)
	switch {
	case loImplied:
		return failIf(t.guarded(count+" "+hiFails), t.subject, text, ctx)
	case hiImplied:
		return failIf(t.guarded(count+" "+loFails), t.subject, text, ctx)
	}
	// The init statement counts once for both bounds, so a nil guard wraps it.
	return t.guardBlock(failIf(fmt.Sprintf("l := %s; l %s || l %s", count, loFails, hiFails), t.subject, text, ctx))
}

// minMaxLengthCheck renders @minLength or @maxLength on a string or bytes
// value, failing it by its side's comparison of the length with n; a bound
// every length meets renders nothing.
func minMaxLengthCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	sides, _ := semantic.BoundSides(d.Name)
	args := semantic.BoundArgs(d)
	if !t.primIs(prims.String, prims.Bytes) || len(args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(args[0])
	if !ok || semantic.BoundImpliedByType(t.prim, d, 0) {
		return ""
	}
	label := "length greater than"
	if sides[0].Lower {
		label = "length less than"
	}
	cond := fmt.Sprintf("%s %s %d", lengthCount(t, ctx), sides[0].FailOp(), n)
	return failIf(t.guarded(cond), t.subject, fmt.Sprintf("%s %d", label, n), ctx)
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
