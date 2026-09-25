package golang

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// itemsBoundCheck renders @minItems/@maxItems as a len() bound on an array or map.
func itemsBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, ctx emitCtx) string {
	if f.Type == nil || len(d.Args) != 1 || (!f.Type.Array && f.Type.Map == nil) {
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
	cond := fmt.Sprintf("len(%s) %s %d", access, flip, n)
	msg := fmt.Sprintf(`"%s: %s %d"`, fieldWireName(f), label, n)
	check := ifReturnf(cond, msg, ctx)
	// Nil is the valid absent/null value of an optional or @nullable collection.
	if fieldNeedsNilGuard(f) {
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, check)
	}
	return check
}

// uniqueItemsCheck renders @uniqueItems on an array as a dedupe map keyed by
// element, inside its own block so each check's `seen` stays local.
func uniqueItemsCheck(f *ast.Field, access string, ctx emitCtx) string {
	if f.Type == nil || !f.Type.Array {
		return ""
	}
	elem := goType(f.Type.ElemTypeRef(), ctx.resolver.Resolver, nil)
	ctx.uses["fmt"] = true
	// The element type keys the map and may name another package.
	f.Type.WalkNamedRefs(ctx.resolver.CrossPkg.importsInto(ctx.uses))
	return fmt.Sprintf(`{
seen := make(map[%s]struct{}, len(%s))
for _, item := range %s {
if _, dup := seen[item]; dup {
return fmt.Errorf("%s: items must be unique")
}
seen[item] = struct{}{}
}
}`, elem, access, access, fieldWireName(f))
}

// maxSizeCheck renders @maxSize on a file field as a nil-guarded bound on its Size.
func maxSizeCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isFileField(f) || len(d.Args) != 1 {
		return ""
	}
	bytes, ok := semantic.SizeArg(d.Args[0])
	if !ok || bytes <= 0 {
		return ""
	}
	cond := fmt.Sprintf("%s != nil && %s.Size > %d", access, access, bytes)
	msg := fmt.Sprintf(`"%s: file size exceeds %d bytes"`, fieldWireName(f), bytes)
	return ifReturnf(cond, msg, ctx)
}

// mimeTypesCheck renders @mimeTypes on a file field as a switch over the
// upload's Content-Type; an absent upload passes.
func mimeTypesCheck(f *ast.Field, access string, d *ast.Decorator, ctx emitCtx) string {
	if !isFileField(f) {
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
}`, access, access, strings.Join(cases, ", "), fieldWireName(f))
}
