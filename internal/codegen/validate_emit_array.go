// Array / file validators: @minItems/@maxItems/@uniqueItems/@maxSize/@mimeTypes.
package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func itemsBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, uses map[string]bool) string {
	if f.Type == nil || !f.Type.Array || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		return ""
	}
	flip := "<"
	if op == "<=" {
		flip = ">"
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("len(%s) %s %d", access, flip, n)
	msg := fmt.Sprintf(`"%s: %s %d"`, f.Name, label, n)
	check := ifReturnf(cond, msg)
	if f.Type.Optional {
		return fmt.Sprintf("if %s != nil {\n\t%s\n}", access, indentBlock(check))
	}
	return check
}

// uniqueItemsCheck handles `@uniqueItems` on array fields. The emitted
// loop scans for duplicates with a map keyed on the element value;
// that works for any comparable element type - primitives, strings,
// fixed-size structs.
//
// `json.RawMessage` (the Go type for `any`) is a `[]byte` named slice,
// which is NOT comparable as a map key. We special-case it to use
// `string(item)` for the key so a `tags any[] @uniqueItems` chain
// still emits compile-clean dedupe code without pulling extra
// imports - `string([]byte)` is a built-in conversion.
//
// Other slice / map / func element types stay un-checked because the
// generated code would not compile.
//
// A bare block scopes `seen` to this check so multiple @uniqueItems
// validators on the same struct don't shadow each other; `return` still
// escapes back to the enclosing Validate() method.
func uniqueItemsCheck(f *ast.Field, access string, uses map[string]bool) string {
	if f.Type == nil || !f.Type.Array {
		return ""
	}
	elem := arrayElemType(f.Type)
	if !isComparableElem(elem) {
		// `any` (Go: `interface{}`) IS comparable in the
		// language sense - but only when its dynamic type is
		// itself comparable. The runtime `==` over interfaces
		// panics for slices / maps / funcs. Skip the
		// auto-emitted dedupe loop for those element types and
		// let logic deduplicate by-shape if it matters.
		return ""
	}
	uses["fmt"] = true
	return fmt.Sprintf(`{
seen := make(map[%s]struct{}, len(%s))
for _, item := range %s {
if _, dup := seen[item]; dup {
return fmt.Errorf("%s: items must be unique")
}
seen[item] = struct{}{}
}
}`, elem, access, access, f.Name)
}

// ----- file --------------------------------------------------------------

// maxSizeCheck handles `@maxSize(<size>)` on `file` fields. The argument
// may be a Size literal (`5MB`, `2KB`, `1024B`) or a bare integer count
// of bytes. Emits a nil-guarded comparison against `*multipart.FileHeader.Size`.
// On non-file fields the decorator is silently skipped.
func maxSizeCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isFileField(f) || len(d.Args) != 1 {
		return ""
	}
	bytes, ok := sizeArg(d.Args[0])
	if !ok || bytes <= 0 {
		return ""
	}
	uses["fmt"] = true
	cond := fmt.Sprintf("%s != nil && %s.Size > %d", access, access, bytes)
	msg := fmt.Sprintf(`"%s: file size exceeds %d bytes"`, f.Name, bytes)
	return ifReturnf(cond, msg)
}

// mimeTypesCheck handles `@mimeTypes(["a/b", "c/d"])` on `file` fields.
// Emits a switch on the upload's Content-Type header rejecting any value
// outside the allowlist. The check is nil-guarded so a missing optional
// upload is allowed by this decorator; drop the `?` suffix on the field
// type to force presence (required-by-default).
func mimeTypesCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isFileField(f) || len(d.Args) != 1 {
		return ""
	}
	mimes, ok := stringArrayArg(d.Args[0])
	if !ok || len(mimes) == 0 {
		return ""
	}
	uses["fmt"] = true
	cases := make([]string, len(mimes))
	for i, m := range mimes {
		cases[i] = strconv.Quote(m)
	}
	return fmt.Sprintf(`if %s != nil {
switch %s.Header.Get("Content-Type") {
case %s:
default:
return fmt.Errorf("%s: disallowed content type")
}
}`, access, access, strings.Join(cases, ", "), f.Name)
}
