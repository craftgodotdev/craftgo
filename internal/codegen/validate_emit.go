package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// This file collects every function that produces Go source for a
// validator. Each per-decorator emitter is paired with a comment
// explaining (a) what type-shapes it accepts and (b) what generated
// code it produces. Three cross-cutting helpers - [shape],
// [ifReturnf], and the enum/typeParam/nested call emitters - are
// shared across multiple validators and live at the top of the file.

// shape returns Go source for a field-level check, picking the right
// per-form scaffold (loop / nil-guard / bare). The body builder is
// invoked once with an "element expression" that the body can use as
// the concrete value to inspect:
//
//   - array  → `access[i]` inside `for i := range access {}`
//   - opt    → `*access`   inside `if access != nil {}`
//   - single → `access`    with no wrapping
//
// The body is responsible for any `return ...` it needs; the wrapper
// merely delivers control to it for each element.
func shape(f *ast.Field, access string, body func(elem string) string) string {
	switch {
	case f.Type != nil && f.Type.Array:
		return fmt.Sprintf("for i := range %s {\n%s\n}", access, body(access+"[i]"))
	case f.Type != nil && f.Type.Optional:
		// Parenthesise the dereferenced access so callers can prefix
		// it with operators (`len(...)` / `&` / method calls) without
		// running into Go's precedence rules. `(*v.Avatar).Validate()`
		// works; `*v.Avatar.Validate()` parses as `*(v.Avatar.Validate())`
		// and tries to deref the returned `error`.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, body("(*"+access+")"))
	default:
		return body(access)
	}
}

// ifReturnf assembles a single multi-line `if cond { return fmt.Errorf(msg) }`
// block. Centralised here so every per-decorator emitter has identical
// output formatting (go/format normalises whitespace afterwards).
func ifReturnf(cond, msg string) string {
	return fmt.Sprintf("if %s {\n\treturn fmt.Errorf(%s)\n}", cond, msg)
}

// ----- presence ----------------------------------------------------------

// requiredKind picks the right Go conditional for an absent value.
// The empty-string sentinel signals "this field type has no obvious
// empty value" so the caller drops the check rather than emitting a
// no-op. `@nullable` upgrades the field to a pointer; the nil check
// covers both "absent from JSON" and "explicit null".
func requiredKind(f *ast.Field, access string) string {
	// `@required` enforces ONLY non-null / non-undefined. Empty
	// strings, zero numerics, empty arrays / maps are allowed; pair
	// with `@length(1, …)` / `@min(1)` / `@minItems(1)` when the
	// contract needs them.
	//
	// For non-pointer scalar types (`string`, `int`, `bool`, …) the
	// JSON decoder already rejects wire `null` with an unmarshal
	// error, so no validate-time check is needed; the diagnostic
	// surfaces at the framework boundary instead. For pointer types
	// (`T?` / `T @nullable`) and `any` we DO need the check - the
	// decoder happily accepts `null` and leaves it as a nil pointer
	// or the literal 4-byte `null` `json.RawMessage`.
	if f.Type == nil {
		return ""
	}
	if f.Type.Optional || goFieldIsPointer(f) {
		return access + " == nil"
	}
	if f.Type.Named != nil && f.Type.Named.Name.String() == "any" {
		// `any` lands on Go's empty interface; the codec leaves it
		// nil for absent fields and for explicit JSON `null` (the
		// decoder collapses both into the zero interface value).
		// `@required` rejects either by checking for nil.
		return access + " == nil"
	}
	return ""
}

// requiredCheck assembles the `@required` block, or returns "" when the
// field type doesn't have a defined empty value.
func requiredCheck(f *ast.Field, access string, uses map[string]bool) string {
	cond := requiredKind(f, access)
	if cond == "" {
		return ""
	}
	uses["fmt"] = true
	return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, f.Name))
}

// requiredCheckEnumAware adds enum support on top of `requiredCheck`. An
// enum-typed field's empty value depends on its underlying base:
// string-valued enums (and bare-value enums, which we render as
// strings) compare against `""`; int-valued enums compare against `0`.
// The check is skipped for arrays / maps / pointers - those reuse the
// generic `requiredCheck` path with len/nil semantics.
func requiredCheckEnumAware(f *ast.Field, access string, pkg *semantic.Package, uses map[string]bool) string {
	if f != nil && f.Type != nil && !f.Type.Array && !f.Type.Optional && f.Type.Map == nil && f.Type.Named != nil {
		if ed, ok := pkg.Enums[f.Type.Named.Name.String()]; ok {
			cond := access + ` == ""`
			if firstEnumKind(ed) == ast.EnumInt {
				cond = access + " == 0"
			}
			uses["fmt"] = true
			return ifReturnf(cond, fmt.Sprintf(`"%s: required"`, f.Name))
		}
	}
	return requiredCheck(f, access, uses)
}

// ----- string ------------------------------------------------------------

// lengthCheck handles `@length(min, max)` for string fields. The check
// supports both required (`string`) and optional (`string?`) fields:
// optional fields get a nil-guarded prefix and the value access is
// dereferenced once before the length probe.
func lengthCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := intArg(d.Args[0])
	hi, ok2 := intArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	// Avoid the `if X != nil && l := len(*X); ...` form - Go forbids
	// `:=` inside an `&&` expression. Inline `len(...)` twice instead;
	// the second call is constant-folded by the compiler when the
	// argument is a simple deref.
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("l := len(%s); l < %d || l > %d", val, lo, hi)
	} else {
		cond = fmt.Sprintf("%s(len(%s) < %d || len(%s) > %d)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%s: length out of range [%d, %d]"`, f.Name, lo, hi)
	return ifReturnf(cond, msg)
}

// minMaxLengthCheck handles `@minLength(n)` and `@maxLength(n)`.
// Optional string fields are handled the same way as `lengthCheck` -
// nil-guard plus pointer deref.
func minMaxLengthCheck(f *ast.Field, access string, d *ast.Decorator, kind string, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok {
		return ""
	}
	op, label := "<", "less than"
	if kind == "max" {
		op, label = ">", "greater than"
	}
	uses["fmt"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%slen(%s) %s %d", guard, val, op, n)
	msg := fmt.Sprintf(`"%s: length %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// patternCheck handles `@pattern("regex")`. The regex is compiled inline
// via `regexp.MustCompile` for v1; a future revision can hoist it to a
// package-level `var` to amortise compile cost.
func patternCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	s, ok := stringArg(d.Args[0])
	if !ok {
		return ""
	}
	uses["fmt"] = true
	uses["regexp"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s!regexp.MustCompile(`%s`).MatchString(%s)", guard, s, val)
	msg := fmt.Sprintf(`"%s: does not match pattern"`, f.Name)
	return ifReturnf(cond, msg)
}

// formatCheck handles `@format(name)` for the catalogue of standard
// formats listed in the README. Each name maps to a built-in regex
// evaluated at request time. The argument may be either a quoted string
// (`@format("email")`) or a bare identifier (`@format(email)`) - both
// forms appear in the existing fixtures and we accept either to avoid
// surprising regressions. Unknown names are silently skipped - projects
// can extend with `@pattern("...")` for niche cases.
func formatCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	name := stringOrIdentArg(d.Args[0])
	if name == "" {
		return ""
	}
	pattern := formatPatterns[name]
	if pattern == "" {
		return ""
	}
	uses["fmt"] = true
	uses["regexp"] = true
	val := stringValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s!regexp.MustCompile(`%s`).MatchString(%s)", guard, pattern, val)
	msg := fmt.Sprintf(`"%s: not a valid %s"`, f.Name, name)
	return ifReturnf(cond, msg)
}

// formatPatterns is the regex catalogue referenced by `@format(...)`.
// Definitions are pragmatic, not RFC-grade; use `@pattern("...")`
// when stricter parsing is required.
//
// `datetime` is RFC 3339 (a strict subset of ISO 8601); `creditcard`
// uses a length-only check because Luhn validation requires loop logic
// that doesn't fit a single regex; `json` accepts any non-empty string
// and defers structural validation to the JSON decoder.
var formatPatterns = map[string]string{
	"email":      `^[^@\s]+@[^@\s]+\.[^@\s]+$`,
	"url":        `^https?://[^\s]+$`,
	"uri":        `^[a-zA-Z][a-zA-Z0-9+.-]*:[^\s]+$`,
	"uuid":       `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
	"hostname":   `^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`,
	"ipv4":       `^(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}$`,
	"ipv6":       `^([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$|^::1$|^::$|^([0-9a-fA-F]{1,4}:){1,7}:$|^:(:[0-9a-fA-F]{1,4}){1,7}$|^([0-9a-fA-F]{1,4}:){1,6}(:[0-9a-fA-F]{1,4}){1}$`,
	"phone":      `^\+?[0-9 ()-]{6,20}$`,
	"datetime":   `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$`,
	"date":       `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`,
	"time":       `^[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})?$`,
	"cidr":       `^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`,
	"mac":        `^([0-9a-fA-F]{2}[:-]){5}[0-9a-fA-F]{2}$`,
	"creditcard": `^[0-9]{12,19}$`,
	"base64":     `^[A-Za-z0-9+/]+={0,2}$`,
	"hexcolor":   `^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`,
	"json":       `^.+$`,
}

// ----- numeric -----------------------------------------------------------

// numericValueExpr is the numeric counterpart to [stringValueExpr]:
// for a pointer-typed numeric field (T? or `T @nullable`) it
// derefs once so callers can drop it straight into a `<` / `>` /
// `%` comparison; plain value fields pass through untouched. The
// returned expression is always paired with [optionalGuard] so the
// deref is gated by a nil-check.
func numericValueExpr(f *ast.Field, access string) string {
	if goFieldIsPointer(f) {
		return "*" + access
	}
	return access
}

// numericBoundCheck handles `@min(n)` / `@max(n)`.
func numericBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 1 {
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
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s %d", guard, val, flip, n)
	msg := fmt.Sprintf(`"%s: %s %d"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// rangeCheck combines @min and @max into one bounded comparison.
// Pointer fields (T? / `T @nullable`) get the same nil-guard +
// deref treatment as [numericBoundCheck].
func rangeCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := intArg(d.Args[0])
	hi, ok2 := intArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("%s < %d || %s > %d", val, lo, val, hi)
	} else {
		// Same pattern as the optional-string `lengthCheck`: avoid
		// `init; cond` syntax inside `&&` by inlining the bounds
		// twice. Compiler folds the duplicate deref.
		cond = fmt.Sprintf("%s(%s < %d || %s > %d)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%s: out of range [%d, %d]"`, f.Name, lo, hi)
	return ifReturnf(cond, msg)
}

// signCheck handles `@positive` (value > 0) and `@negative` (value < 0)
// on numeric fields. Both produce a one-line conditional with no decorator
// arguments - unlike `@min` they don't carry a bound, so the helper is a
// pure dispatch on the kind string.
func signCheck(f *ast.Field, access, kind string, uses map[string]bool) string {
	if !isNumericField(f) {
		return ""
	}
	uses["fmt"] = true
	op, label := "<=", "must be positive"
	if kind == "negative" {
		op, label = ">=", "must be negative"
	}
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s 0", guard, val, op)
	msg := fmt.Sprintf(`"%s: %s"`, f.Name, label)
	return ifReturnf(cond, msg)
}

// multipleOfCheck handles `@multipleOf(n)` on integer fields. Floats are
// excluded because `%` is integer-only in Go and a runtime modulus on a
// float is rarely what designers intend (rounding error). A future revision
// can layer a tolerance-based check for floats.
func multipleOfCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isIntegerField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := intArg(d.Args[0])
	if !ok || n == 0 {
		return ""
	}
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s%%%d != 0", guard, val, n)
	msg := fmt.Sprintf(`"%s: must be a multiple of %d"`, f.Name, n)
	return ifReturnf(cond, msg)
}

// ----- array -------------------------------------------------------------

// itemsBoundCheck handles `@minItems(n)` and `@maxItems(n)`.
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
	return ifReturnf(cond, msg)
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
// upload is allowed by this decorator (combine with `@required` to force
// presence).
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

// ----- nested / generic / enum ------------------------------------------

// enumValueCheck emits a switch-case rejecting any value that is not
// in the field's enum declaration. Only fires for fields whose declared
// type names a `pkg.Enums` entry. The field's array / optional / single
// shape is handled by the [shape] helper.
func enumValueCheck(f *ast.Field, pkg *semantic.Package, uses map[string]bool) string {
	if pkg == nil || f == nil || f.Type == nil || f.Type.Map != nil || f.Type.Named == nil {
		return ""
	}
	ed, ok := pkg.Enums[f.Type.Named.Name.String()]
	if !ok || len(ed.Values) == 0 {
		return ""
	}
	uses["fmt"] = true
	access := "v." + GoFieldName(f.Name)
	caseList := enumCaseList(ed)
	msg := fmt.Sprintf(`"%s: invalid %s value"`, f.Name, ed.Name)
	return shape(f, access, func(elem string) string {
		return fmt.Sprintf(`switch %s {
case %s:
default:
return fmt.Errorf(%s)
}`, elem, caseList, msg)
	})
}

// enumCaseList renders the comma-separated list of fully-qualified
// constant names matching `<EnumName><PascalCase(ValueName)>`, the same
// naming convention `enums.go` uses.
func enumCaseList(ed *ast.EnumDecl) string {
	parts := make([]string, 0, len(ed.Values))
	for _, v := range ed.Values {
		parts = append(parts, ed.Name+GoFieldName(v.Name))
	}
	return strings.Join(parts, ", ")
}

// typeParamValidateCall emits the runtime type-assertion path for a
// field whose declared type is a generic parameter (`T`, `T[]`, `T?`).
// Because Go cannot statically prove T has a Validate() method, the
// generated code probes via `any(x).(interface{ Validate() error })`
// and only invokes Validate when the assertion succeeds. Concrete
// instances that happen to satisfy the interface are validated; pure
// primitive instances simply skip the check.
//
// We always pass a *pointer* to the assertion. `Validate()` lands on
// the pointer receiver in our generated code, so `any(value)` would
// miss any concrete struct whose method is declared on `*T`. The shape
// helper hands us the value-form expression for each form; we wrap it
// with `&` for arrays/single, but optional fields are already a `*T`
// so we use the pointer access as-is.
func typeParamValidateCall(f *ast.Field) string {
	access := "v." + GoFieldName(f.Name)
	return shape(f, access, func(elem string) string {
		probe := "&" + elem
		if f.Type.Optional {
			probe = access
		}
		return fmt.Sprintf(`if vv, ok := any(%s).(interface{ Validate() error }); ok {
if err := vv.Validate(); err != nil {
return err
}
}`, probe)
	})
}

// nestedValidateCall emits a recursive `field.Validate()` call when a
// field's declared type is another user-defined struct (or a generic
// instance, since those now carry Validate too). Maps are skipped:
// map values need range traversal that v1 doesn't generate.
//
// We bypass the generic [shape] helper for optional fields so the
// emitted call reads `v.Avatar.Validate()` rather than the noisier
// `(*v.Avatar).Validate()` - Go's method-set rules dispatch through
// the pointer-receiver Validate either way, and the cleaner form is
// what a human would write by hand.
func nestedValidateCall(f *ast.Field, pkg *semantic.Package, uses map[string]bool) string {
	_ = uses
	if pkg == nil || f.Type == nil || f.Type.Map != nil || f.Type.Named == nil {
		return ""
	}
	if _, ok := pkg.Types[f.Type.Named.Name.String()]; !ok {
		return ""
	}
	access := "v." + GoFieldName(f.Name)
	body := func(elem string) string {
		return fmt.Sprintf(`if err := %s.Validate(); err != nil {
return err
}`, elem)
	}
	switch {
	case f.Type.Array:
		return fmt.Sprintf("for i := range %s {\n%s\n}", access, body(access+"[i]"))
	case f.Type.Optional:
		// access is already `*Type`. Method dispatch auto-resolves
		// through the pointer; no explicit deref needed.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, body(access))
	default:
		return body(access)
	}
}
