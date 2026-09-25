package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// itemsBoundCheck renders @minItems/@maxItems as a len() bound on an array or map.
func itemsBoundCheck(t checkTarget, d *ast.Decorator, op, label string, ctx emitCtx) string {
	if (t.cat != semantic.CatArray && t.cat != semantic.CatMap) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(d.Args[0])
	if !ok {
		return ""
	}
	// `@minItems(0)` accepts every length.
	if op == ">=" && n == 0 {
		return ""
	}
	flip := "<"
	if op == "<=" {
		flip = ">"
	}
	cond := fmt.Sprintf("len(%s) %s %d", t.access, flip, n)
	msg := fmt.Sprintf(`"%s: %s %d"`, t.subject, label, n)
	check := ifReturnf(cond, msg, ctx)
	// Nil is the valid absent/null value of an optional or @nullable collection.
	if t.nilGuard {
		return fmt.Sprintf("if %s != nil {\n%s\n}", t.access, check)
	}
	return check
}

// uniqueItemsCheck renders @uniqueItems on an array as a dedupe map keyed by
// element, inside its own block so each check's `seen` stays local.
func uniqueItemsCheck(t checkTarget, ctx emitCtx) string {
	if t.cat != semantic.CatArray {
		return ""
	}
	elem := goType(t.typ.ElemTypeRef(), ctx.resolver.Resolver, nil)
	ctx.uses["fmt"] = true
	// The element type keys the map and may name another package.
	t.typ.WalkNamedRefs(ctx.resolver.CrossPkg.importsInto(ctx.uses))
	return fmt.Sprintf(`{
seen := make(map[%s]struct{}, len(%s))
for _, item := range %s {
if _, dup := seen[item]; dup {
return fmt.Errorf("%s: items must be unique")
}
seen[item] = struct{}{}
}
}`, elem, t.access, t.access, t.subject)
}

// maxSizeCheck renders @maxSize on a file as a nil-guarded bound on its Size.
func maxSizeCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !t.primIs(prims.File) || len(d.Args) != 1 {
		return ""
	}
	bytes, ok := semantic.SizeArg(d.Args[0])
	if !ok || bytes <= 0 {
		return ""
	}
	cond := fmt.Sprintf("%s != nil && %s.Size > %d", t.access, t.access, bytes)
	msg := fmt.Sprintf(`"%s: file size exceeds %d bytes"`, t.subject, bytes)
	return ifReturnf(cond, msg, ctx)
}

// mimeTypesCheck renders @mimeTypes on a file as a switch over the upload's
// Content-Type; an absent upload passes.
func mimeTypesCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !t.primIs(prims.File) {
		return ""
	}
	var cases []string
	for _, mime := range ast.ArgNames(d) {
		cases = append(cases, strconv.Quote(mime.Value))
	}
	if len(cases) == 0 {
		return ""
	}
	ctx.uses["fmt"] = true
	return fmt.Sprintf(`if %s != nil {
switch %s.Header.Get("Content-Type") {
case %s:
default:
return fmt.Errorf("%s: disallowed content type")
}
}`, t.access, t.access, strings.Join(cases, ", "), t.subject)
}
