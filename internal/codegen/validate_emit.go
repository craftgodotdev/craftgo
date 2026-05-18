package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
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
// no-op.
//
// craftgo's "required by default" model: every non-optional field
// gets a presence check automatically. Empty strings / zero numerics
// / empty arrays / maps are allowed (pair with `@length(1, …)` /
// `@min(1)` / `@minItems(1)` when stricter shape is required).
//
// For non-pointer scalar types (`string`, `int`, `bool`, …) the
// JSON decoder already rejects wire `null` with an unmarshal error,
// so no validate-time check is needed; the diagnostic surfaces at
// the framework boundary instead. For pointer types (`T?` /
// `T @nullable`) and `any` we DO need the check - the decoder
// happily accepts `null` and leaves it as a nil pointer or the
// literal 4-byte `null` `json.RawMessage`.
func requiredKind(f *ast.Field, access string) string {
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
		return access + " == nil"
	}
	return ""
}

// requiredCheck assembles the presence-check block, or returns ""
// when the field type doesn't have a defined empty value.
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
// formats. Each entry in [formatValidators] declares the Go imports
// needed and the emit shape (regex, single-expression Go check, or
// init-statement check). The argument may be either a quoted string
// (`@format("email")`) or a bare identifier (`@format(email)`) - both
// accepted. Unknown names skip silently; projects can extend with
// `@pattern("...")` for niche cases.
func formatCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isStringOrOptString(f) || len(d.Args) != 1 {
		return ""
	}
	name := stringOrIdentArg(d.Args[0])
	if name == "" {
		return ""
	}
	v, ok := formatValidators[name]
	if !ok {
		return ""
	}
	for _, imp := range v.imports {
		uses[imp] = true
	}
	uses["fmt"] = true
	val := stringValueExpr(f, access)
	msg := fmt.Sprintf(`"%s: not a valid %s"`, f.Name, v.label)
	if f.Type.Optional {
		// Pointer field: nest the check inside a nil-guard so the
		// init-stmt forms (mail.ParseAddress / time.Parse / ...) only
		// run when a value is present.
		inner := v.emit(val, msg)
		return fmt.Sprintf("if %s != nil {\n\t%s\n}", access, indentBlock(inner))
	}
	return v.emit(val, msg)
}

// indentBlock prefixes every line after the first with a single tab so
// nested if-blocks render with consistent indentation. Used by the
// pointer-field nesting in [formatCheck].
func indentBlock(s string) string {
	return strings.ReplaceAll(s, "\n", "\n\t")
}

// formatValidator binds a `@format(name)` to the Go source that
// validates a value. Two emit shapes are supported:
//
//   - Pure-expression conds (regex, json.Valid, ...): the emitter
//     returns "if <cond> { return ... }" where <cond> is a single Go
//     boolean expression true-when-invalid.
//   - Init-statement conds (net.ParseIP, mail.ParseAddress, ...): the
//     emitter uses Go's `if init; cond` form where the init declares
//     a temp + the cond probes it; pointer fields wrap in a nil-guard
//     to skip the init when the value is absent.
//
// imports lists the Go packages used by the check; they're appended to
// the validate file's import block via [emitCtx.uses].
type formatValidator struct {
	label   string
	imports []string
	emit    func(val, msg string) string
}

// exprFormat builds a [formatValidator] from a single Go boolean
// expression that's true-when-invalid. `condFmt` must contain exactly
// one `%s` placeholder for the value access.
func exprFormat(label string, imports []string, condFmt string) formatValidator {
	return formatValidator{
		label:   label,
		imports: imports,
		emit: func(val, msg string) string {
			cond := fmt.Sprintf(condFmt, val)
			return ifReturnf(cond, msg)
		},
	}
}

// stmtFormat builds a [formatValidator] from a Go init-statement +
// condition pair. `condFmt` must contain `%s` for the value access
// and produce text like `_, _err := f(%s); _err != nil` - the whole
// thing slots into Go's `if init; cond` form.
func stmtFormat(label string, imports []string, condFmt string) formatValidator {
	return formatValidator{
		label:   label,
		imports: imports,
		emit: func(val, msg string) string {
			cond := fmt.Sprintf(condFmt, val)
			return ifReturnf(cond, msg)
		},
	}
}

// regexFormat is shorthand for [exprFormat] with the standard
// regex-MatchString pattern. Kept for the formats where regex is the
// pragmatic choice (uuid, hostname, hexcolor, ...).
func regexFormat(label, pattern string) formatValidator {
	return exprFormat(label, []string{"regexp"},
		"!regexp.MustCompile(`"+pattern+"`).MatchString(%s)")
}

// formatValidators is the canonical catalogue. For RFC compliance the
// network/time/email checks delegate to the Go standard library; the
// rest stay regex for shapes where stdlib has no direct equivalent
// (UUID, hex color, hostname, phone, credit card length).
var formatValidators = map[string]formatValidator{
	// RFC 5322 email - net/mail.ParseAddress accepts the full
	// address-spec grammar (display name + addr-spec); we feed it
	// the raw string so common forms ("a@b.com", "a+tag@b.co.uk")
	// pass while obviously-malformed ones are rejected.
	"email": stmtFormat("email", []string{"net/mail"},
		`_, _err := mail.ParseAddress(%s); _err != nil`),

	// HTTP/HTTPS URLs - net/url.Parse + scheme guard. The bare
	// `url.Parse` is permissive (it accepts `mailto:`, `data:`, ...);
	// we additionally require http/https since the format name
	// implies a web URL.
	"url": stmtFormat("URL", []string{"net/url"},
		`_u, _err := url.Parse(%s); _err != nil || (_u.Scheme != "http" && _u.Scheme != "https")`),

	// RFC 3986 generic URI - any non-empty scheme.
	"uri": stmtFormat("URI", []string{"net/url"},
		`_u, _err := url.Parse(%s); _err != nil || _u.Scheme == ""`),

	// RFC 4122 UUID - format-only check (we don't enforce a
	// specific version digit; consumers can layer @pattern on top
	// when they want strict v4 etc.).
	"uuid": regexFormat("UUID",
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),

	// RFC 1123 hostname - alphanumeric labels with optional hyphens
	// in the middle, separated by dots.
	"hostname": regexFormat("hostname",
		`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`),

	// RFC 791 IPv4 - net.ParseIP + To4 disambiguates from the
	// IPv6 form (which net.ParseIP also accepts).
	"ipv4": stmtFormat("IPv4", []string{"net"},
		`_ip := net.ParseIP(%s); _ip == nil || _ip.To4() == nil`),

	// RFC 4291 IPv6 - parse succeeds AND not a v4 address. Handles
	// `::`, zone IDs, IPv4-mapped (`::ffff:1.2.3.4`), shortened
	// forms - all the cases the previous regex missed.
	"ipv6": stmtFormat("IPv6", []string{"net"},
		`_ip := net.ParseIP(%s); _ip == nil || _ip.To4() != nil`),

	// E.164-ish phone with human-friendly separators. Stricter
	// users should add `@pattern("^\\+\\d{1,15}$")`.
	"phone": regexFormat("phone",
		`^\+?[0-9 ()-]{6,20}$`),

	// RFC 3339 date-time. time.Parse handles fractional seconds,
	// optional offset, and rejects malformed dates (Feb 30 etc.)
	// that the regex previously let through.
	"datetime": stmtFormat("RFC 3339 datetime", []string{"time"},
		`_, _err := time.Parse(time.RFC3339, %s); _err != nil`),

	// RFC 3339 full-date.
	"date": stmtFormat("date", []string{"time"},
		`_, _err := time.Parse(time.DateOnly, %s); _err != nil`),

	// RFC 3339 partial-time. time.TimeOnly is `15:04:05`; offset
	// is not part of partial-time, so we use the dedicated layout.
	"time": stmtFormat("time", []string{"time"},
		`_, _err := time.Parse(time.TimeOnly, %s); _err != nil`),

	// RFC 4632 / RFC 4291 CIDR - net.ParseCIDR handles both v4
	// and v6 with mask-range validation. The previous regex was
	// IPv4-only and didn't validate octet bounds.
	"cidr": stmtFormat("CIDR", []string{"net"},
		`_, _, _err := net.ParseCIDR(%s); _err != nil`),

	// MAC-48 / EUI-64 / 20-octet InfiniBand - net.ParseMAC accepts
	// `:`-separated, `-`-separated, and dot-separated forms across
	// all three lengths.
	"mac": stmtFormat("MAC address", []string{"net"},
		`_, _err := net.ParseMAC(%s); _err != nil`),

	// Length-only credit card number sanity. Luhn checksum needs
	// loop logic (not a single expression); pair with custom logic
	// when stricter validation matters.
	"creditcard": regexFormat("credit card number",
		`^[0-9]{12,19}$`),

	// RFC 4648 §4 standard base64 (with `+/=`). Use `base64url`
	// for the URL-safe alphabet.
	"base64": stmtFormat("base64", []string{"encoding/base64"},
		`_, _err := base64.StdEncoding.DecodeString(%s); _err != nil`),

	// RFC 4648 §5 URL-safe base64 (with `-_=`).
	"base64url": stmtFormat("base64url", []string{"encoding/base64"},
		`_, _err := base64.URLEncoding.DecodeString(%s); _err != nil`),

	// CSS hex color (3 or 6 hex digits, optional `#` prefix).
	"hexcolor": regexFormat("hex color",
		`^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`),

	// RFC 8259 JSON - json.Valid does the full structural parse so
	// we catch bad escapes, unbalanced brackets, etc. that the
	// previous "non-empty" regex passed through.
	"json": exprFormat("JSON", []string{"encoding/json"},
		`!json.Valid([]byte(%s))`),
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

// numericBoundCheck handles the 4 comparison decorators
// `@gt(n)` / `@gte(n)` / `@lt(n)` / `@lte(n)`. `op` is the
// validity predicate the value must satisfy; the emitted condition is
// the NEGATION (true when invalid).
//
//	@gte(0): valid if x >= 0  → fail if x < 0
//	@gt(0):  valid if x > 0   → fail if x <= 0
//	@lte(N): valid if x <= N  → fail if x > N
//	@lt(N):  valid if x < N   → fail if x >= N
//
// Both int and float bound literals are accepted ([numericArg] handles
// the rendering). Float fields with float bounds (`@gte(0.5)` on
// float64) work the same as int-on-int.
func numericBoundCheck(f *ast.Field, access string, d *ast.Decorator, op, label string, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 1 {
		return ""
	}
	n, ok := numericArg(d.Args[0])
	if !ok {
		return ""
	}
	var flip string
	switch op {
	case ">=":
		flip = "<"
	case ">":
		flip = "<="
	case "<=":
		flip = ">"
	case "<":
		flip = ">="
	default:
		return ""
	}
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	cond := fmt.Sprintf("%s%s %s %s", guard, val, flip, n)
	msg := fmt.Sprintf(`"%s: %s %s"`, f.Name, label, n)
	return ifReturnf(cond, msg)
}

// rangeCheck combines @gte and @lte into one bounded comparison.
// Pointer fields (T? / `T @nullable`) get the same nil-guard +
// deref treatment as [numericBoundCheck]. Both int and float bound
// literals accepted.
func rangeCheck(f *ast.Field, access string, d *ast.Decorator, uses map[string]bool) string {
	if !isNumericField(f) || len(d.Args) != 2 {
		return ""
	}
	lo, ok1 := numericArg(d.Args[0])
	hi, ok2 := numericArg(d.Args[1])
	if !ok1 || !ok2 {
		return ""
	}
	uses["fmt"] = true
	val := numericValueExpr(f, access)
	guard := optionalGuard(f, access)
	var cond string
	if guard == "" {
		cond = fmt.Sprintf("%s < %s || %s > %s", val, lo, val, hi)
	} else {
		// Same pattern as the optional-string `lengthCheck`: avoid
		// `init; cond` syntax inside `&&` by inlining the bounds
		// twice. Compiler folds the duplicate deref.
		cond = fmt.Sprintf("%s(%s < %s || %s > %s)", guard, val, lo, val, hi)
	}
	msg := fmt.Sprintf(`"%s: out of range [%s, %s]"`, f.Name, lo, hi)
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

// itemsBoundCheck handles `@minItems(n)` and `@maxItems(n)`. Optional
// arrays (`T[]?`) skip the check when the slice is nil — Go's `len(nil)`
// is 0, so a bare `len(v.X) < 1` would reject the absent case. The
// canonical nil-guard `if v.X != nil { ... }` preserves "absent =
// skipped" for optional shapes.
//
// `@maxItems` doesn't need the guard for correctness (`len(nil) > N` is
// always false) but emit one anyway for symmetry — keeps the optional
// codepath uniform across min/max.
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
	if !ok || len(ed.EnumValues()) == 0 {
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
	enumVals := ed.EnumValues()
	parts := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
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
	if pkg == nil || f.Type == nil {
		return ""
	}
	access := "v." + GoFieldName(f.Name)
	body := func(elem string) string {
		return fmt.Sprintf(`if err := %s.Validate(); err != nil {
return err
}`, elem)
	}
	// Map: walk the values. A map value that is a user-defined type
	// (or an array / optional thereof) carries its own Validate(); the
	// previous early-return left every `map<K, User>` etc. unchecked,
	// silently breaking the recursive-validation contract.
	if f.Type.Map != nil {
		v := f.Type.Map.Value
		if !typeRefHasValidator(v, pkg) {
			return ""
		}
		// Element-access expression for one map value, wrapped in
		// optional / array shape as needed.
		switch {
		case v.Array:
			depth := v.ArrayDepth
			if depth < 1 {
				depth = 1
			}
			inner := emitNestedForLoops("val", depth, body)
			return fmt.Sprintf("for _, val := range %s {\n%s\n}", access, inner)
		case v.Optional:
			return fmt.Sprintf("for _, val := range %s {\nif val != nil {\n%s\n}\n}", access, body("val"))
		default:
			return fmt.Sprintf("for _, val := range %s {\n%s\n}", access, body("val"))
		}
	}
	if f.Type.Named == nil {
		return ""
	}
	if _, ok := pkg.Types[f.Type.Named.Name.String()]; !ok {
		return ""
	}
	switch {
	case f.Type.Array:
		// Multi-dim arrays (`T[][]`, `T[][][]`) need one for-loop per
		// dimension; a single loop would call `Validate()` on a slice,
		// not the element. ArrayDepth (0 means 1-dim "T[]") drives the
		// nesting depth.
		depth := f.Type.ArrayDepth
		if depth < 1 {
			depth = 1
		}
		return emitNestedForLoops(access, depth, body)
	case f.Type.Optional:
		// access is already `*Type`. Method dispatch auto-resolves
		// through the pointer; no explicit deref needed.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, body(access))
	default:
		return body(access)
	}
}

// typeRefHasValidator reports whether the type referenced by `t`
// (after stripping any array / optional decoration) is a user-defined
// struct that carries a generated Validate() method. Map keys go
// through scalar-decorator emission elsewhere, so this only inspects
// the value side.
func typeRefHasValidator(t *ast.TypeRef, pkg *semantic.Package) bool {
	if t == nil || t.Map != nil || t.Named == nil {
		return false
	}
	_, ok := pkg.Types[t.Named.Name.String()]
	return ok
}

// emitNestedForLoops produces `depth` nested `for i0 := range x` loops
// where the innermost body sees the deepest element expression
// (`x[i0][i1]...[i{depth-1}]`). Used by [nestedValidateCall] for
// multi-dimensional arrays of struct-typed elements.
func emitNestedForLoops(access string, depth int, body func(elem string) string) string {
	// Build the deepest element path that the body operates on.
	elem := access
	for d := 0; d < depth; d++ {
		elem += fmt.Sprintf("[i%d]", d)
	}
	out := body(elem)
	// Wrap loops outside-in. Loop d ranges over `access[i0]…[i{d-1}]`.
	for d := depth - 1; d >= 0; d-- {
		rangeOver := access
		for k := 0; k < d; k++ {
			rangeOver += fmt.Sprintf("[i%d]", k)
		}
		out = fmt.Sprintf("for i%d := range %s {\n%s\n}", d, rangeOver, out)
	}
	return out
}
