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
// map, failing it when `len failOp n` holds; a bound every count meets
// renders nothing.
func itemsBoundCheck(t checkTarget, d *ast.Decorator, failOp, label string, ctx emitCtx) string {
	if (t.cat != semantic.CatArray && t.cat != semantic.CatMap) || len(d.Args) != 1 {
		return ""
	}
	n, ok := semantic.IntArg(d.Args[0])
	if !ok || semantic.BoundImpliedByType(t.prim, d, 0) {
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
	// The element type keys the map and may name another package.
	elem := ctx.imports.goType(t.typ.ElemTypeRef())
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

// mimeTypesCheck renders @mimeTypes on a file as a match of the upload's media type, its
// parameters and case aside, against each type or `type/*` range; an absent upload passes.
func mimeTypesCheck(t checkTarget, d *ast.Decorator, ctx emitCtx) string {
	if !t.primIs(prims.File) {
		return ""
	}
	var conds []string
	for _, arg := range ast.ArgNames(d) {
		mt := strings.ToLower(arg.Value)
		switch {
		case mt == "*/*":
			return ""
		case strings.HasSuffix(mt, "/*"):
			ctx.imports.use("strings")
			conds = append(conds, "strings.HasPrefix(_mt, "+strconv.Quote(strings.TrimSuffix(mt, "*"))+")")
		default:
			conds = append(conds, "_mt == "+strconv.Quote(mt))
		}
	}
	if len(conds) == 0 {
		return ""
	}
	ctx.imports.use("mime")
	return t.guardBlock(fmt.Sprintf(`switch _mt, _, _ := mime.ParseMediaType(%s.Header.Get("Content-Type")); {
case %s:
default:
return %s
}`, t.access, strings.Join(conds, ", "), errorf(t.subject, "disallowed content type", ctx)))
}
