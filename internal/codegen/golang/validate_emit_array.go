package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// itemsBoundCheck renders @minItems/@maxItems as a len() bound on an array or
// map, failing it when `len failOp n` holds.
func itemsBoundCheck(t checkTarget, d *ast.Decorator, failOp, label string, ctx emitCtx) string {
	if (t.cat != semantic.CatArray && t.cat != semantic.CatMap) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(d.Args[0])
	if !ok || !countCanFail(failOp, n) {
		return ""
	}
	cond := fmt.Sprintf("len(%s) %s %d", t.access, failOp, n)
	return t.guardBlock(failIf(cond, t.subject, fmt.Sprintf("%s %d", label, n), ctx))
}

// uniqueItemsCheck renders @uniqueItems on an array as a dedupe map keyed by
// element, inside its own block so each check's `seen` stays local.
func uniqueItemsCheck(t checkTarget, ctx emitCtx) string {
	if t.cat != semantic.CatArray {
		return ""
	}
	elem := goType(t.typ.ElemTypeRef(), ctx.resolver.Resolver, nil)
	// The element type keys the map and may name another package.
	t.typ.WalkNamedRefs(ctx.resolver.CrossPkg.importsInto(ctx.uses))
	return fmt.Sprintf(`{
seen := make(map[%s]struct{}, len(%s))
for _, item := range %s {
if _, dup := seen[item]; dup {
return %s
}
seen[item] = struct{}{}
}
}`, elem, t.access, t.access, errorf(t.subject, "items must be unique", ctx))
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
	cond := fmt.Sprintf("%s.Size > %d", t.access, bytes)
	return failIf(t.guarded(cond), t.subject, fmt.Sprintf("file size exceeds %d bytes", bytes), ctx)
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
	return t.guardBlock(fmt.Sprintf(`switch %s.Header.Get("Content-Type") {
case %s:
default:
return %s
}`, t.access, strings.Join(cases, ", "), errorf(t.subject, "disallowed content type", ctx)))
}
