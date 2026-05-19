package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// runValidateGen returns the rendered validate.go source for `src`. The
// helper centralises the pkg → tempdir → file-read dance so individual
// validator tests stay focused on the assertion that matters.
func runValidateGen(t *testing.T, src string) string {
	t.Helper()
	pkg := analyze(t, src)
	dir := t.TempDir()
	if err := GenerateValidators(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "validate.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	return string(out)
}

func TestValidateRequired(t *testing.T) {
	// Required-by-default: a non-optional field enforces non-null
	// only - empty string is allowed unless paired with `@length` /
	// `@minLength`. For a non-pointer `string` the JSON decoder
	// already rejects wire `null`, so the validator emits NO check
	// on this field.
	src := runValidateGen(t, `package design
type X { name string }`)
	if strings.Contains(src, `v.Name == ""`) {
		t.Errorf("required-by-default must not emit an empty-string check on plain string:\n%s", src)
	}
	// On `any` the decoder accepts the literal 4-byte `null` slice
	// silently, so the validator does need to fire.
	srcAny := runValidateGen(t, `package design
type X { data any }`)
	if !strings.Contains(srcAny, `v.Data == nil`) {
		t.Errorf("required-by-default on any should reject nil interface:\n%s", srcAny)
	}
}

func TestValidateLengthMinMax(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    a string @length(2, 10)
    b string @minLength(1)
    c string @maxLength(50)
}`)
	for _, want := range []string{"len(v.A)", "len(v.B) < 1", "len(v.C) > 50"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidateNumericBounds(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    age   int @gte(0)
    score int @lte(100)
    n     int @range(1, 99)
}`)
	for _, want := range []string{"v.Age < 0", "v.Score > 100", "v.N < 1 || v.N > 99"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidateNumericBoundsOptional(t *testing.T) {
	// Regression: `T?` numeric fields must still emit @min/@max/@range/
	// @positive/@negative/@multipleOf checks, nil-guarded so the deref
	// runs only when a value is present.
	src := runValidateGen(t, `package design
type X {
    age   int?     @gte(0) @lte(150)
    score int?     @range(0, 100)
    step  int?     @positive @multipleOf(5)
    delta float64? @negative
}`)
	for _, want := range []string{
		"v.Age != nil && *v.Age < 0",
		"v.Age != nil && *v.Age > 150",
		"v.Score != nil && (*v.Score < 0 || *v.Score > 100)",
		"v.Step != nil && *v.Step <= 0",
		"v.Step != nil && *v.Step%5 != 0",
		"v.Delta != nil && *v.Delta >= 0",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidateFloatBounds(t *testing.T) {
	// C1: float bound literals must emit checks (previously dropped
	// silently because intArg only matched IntLit).
	src := runValidateGen(t, `package design
type X {
    rate  float64 @gte(0.5) @lte(1.5)
    tax   float32 @range(0.0, 0.99)
    step  float64 @gt(0.1) @lt(0.9)
}`)
	for _, want := range []string{
		"v.Rate < 0.5",
		"v.Rate > 1.5",
		"v.Tax < 0 || v.Tax > 0.99",
		"v.Step <= 0.1",
		"v.Step >= 0.9",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidateStrictBounds(t *testing.T) {
	// Sprint 2 S2: @gt and @lt are strict variants of @gte / @lte.
	// Validity = `x > N` / `x < N`; codegen emits the inverted form
	// `x <= N` / `x >= N` as the failure condition.
	src := runValidateGen(t, `package design
type X {
    pos  int @gt(0)
    bnd  int @lt(100)
    rng  int @gt(0) @lt(100)
}`)
	for _, want := range []string{
		"v.Pos <= 0",        // @gt(0) fails when x <= 0
		"v.Bnd >= 100",      // @lt(100) fails when x >= 100
		"v.Rng <= 0",        // @gt(0) part of strict-both pair
		"v.Rng >= 100",      // @lt(100) part of strict-both pair
		"must be greater than 0",
		"must be less than 100",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidatePositive(t *testing.T) {
	src := runValidateGen(t, `package design
type X { age int @positive }`)
	if !strings.Contains(src, "v.Age <= 0") {
		t.Errorf("positive check missing:\n%s", src)
	}
	if !strings.Contains(src, "must be positive") {
		t.Errorf("positive label missing:\n%s", src)
	}
}

func TestValidateNegative(t *testing.T) {
	src := runValidateGen(t, `package design
type X { delta int @negative }`)
	if !strings.Contains(src, "v.Delta >= 0") {
		t.Errorf("negative check missing:\n%s", src)
	}
}

func TestValidateMultipleOf(t *testing.T) {
	src := runValidateGen(t, `package design
type X { count int @multipleOf(5) }`)
	if !strings.Contains(src, "v.Count%5 != 0") {
		t.Errorf("multipleOf check missing:\n%s", src)
	}
}

func TestValidateMultipleOfRejectsFloat(t *testing.T) {
	// Float fields with @multipleOf are rejected at the semantic
	// layer. Earlier behaviour silently dropped the check at codegen
	// (Go's `%` operator is integer-only), leaving the runtime
	// validator inconsistent with the OpenAPI side which still
	// emitted `multipleOf: 0.5`.
	src := tryRunValidateGen(t, `package design
type X { ratio float64 @multipleOf(2) }`)
	if src != "" && strings.Contains(src, "v.Ratio%") {
		t.Errorf("float @multipleOf should be rejected, codegen emitted:\n%s", src)
	}
}

// tryRunValidateGen mirrors [runValidateGen] but returns "" instead
// of fatal-ing when the analyzer rejects the source. Used by negative
// tests that pin "this design no longer compiles" without bringing
// the test process down.
func tryRunValidateGen(t *testing.T, src string) string {
	t.Helper()
	pkg, diags := semantic.Analyze([]*ast.File{mustParse(t, src)})
	if len(diags) > 0 {
		return ""
	}
	dir := t.TempDir()
	if err := GenerateValidators(pkg, dir); err != nil {
		return ""
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "validate.go"))
	if err != nil {
		return ""
	}
	return string(out)
}

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	p := craftparser.New("test.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse failed: %v", d)
	}
	return f
}

func TestValidateUniqueItems(t *testing.T) {
	src := runValidateGen(t, `package design
type X { tags string[] @uniqueItems }`)
	if !strings.Contains(src, "make(map[string]struct{}, len(v.Tags))") {
		t.Errorf("uniqueItems map missing:\n%s", src)
	}
	if !strings.Contains(src, "items must be unique") {
		t.Errorf("uniqueItems message missing:\n%s", src)
	}
}

func TestValidateMapStructValueRecurses(t *testing.T) {
	// Previously every `map<K, V>` short-circuited recursive
	// validation regardless of V's shape. A `map<string, User>` left
	// `User.Validate()` uncalled — Email format / length / pattern
	// checks silently skipped for every map entry. The fix walks
	// values for user-defined types (including array / optional of
	// user types).
	src := runValidateGen(t, `package design
type User { id string @minLength(1) }
type Catalog {
    plain   map<string, User>
    arrayV  map<string, User[]>
    optV    map<string, User?>
}`)
	for _, want := range []string{
		// plain: range values, validate each
		"for _, val := range v.Plain",
		"val.Validate()",
		// array value: outer loop + inner loop
		"for _, val := range v.ArrayV",
		"for i0 := range val",
		"val[i0].Validate()",
		// optional value: range + nil-guard
		"for _, val := range v.OptV",
		"if val != nil",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("map value recursion missing %q:\n%s", want, src)
		}
	}
	mustParseGo(t, src)
}

func TestValidateRegexHoisted(t *testing.T) {
	// Patterns and regex-backed format catalogue entries must compile
	// ONCE at package init via `var _pattern0 = regexp.MustCompile(...)`
	// — the previous inline form recompiled the regex on every
	// Validate() call, paying the parser cost per request.
	src := runValidateGen(t, `package design
type X {
    code   string @pattern("^[A-Z]+$")
    sku    string @pattern("^[A-Z]+$")
    uuidV  string @format(uuid)
    color  string @format(hexcolor)
}`)
	// Package-level var block exists.
	if !strings.Contains(src, "var (") || !strings.Contains(src, "= regexp.MustCompile(") {
		t.Errorf("expected package-level regex var block:\n%s", src)
	}
	// Validate() body references the interned var, not MustCompile.
	if strings.Contains(src, "regexp.MustCompile(") {
		// Allowed only inside the var block; check it doesn't leak
		// into func bodies by counting occurrences vs unique patterns.
		nMustCompile := strings.Count(src, "regexp.MustCompile(")
		// 3 patterns total: user pattern ("^[A-Z]+$"), uuid, hexcolor.
		// The two `@pattern("^[A-Z]+$")` deduplicate into a single var.
		if nMustCompile != 3 {
			t.Errorf("expected 3 unique regex vars (^[A-Z]+$ deduped, uuid, hexcolor), got %d:\n%s", nMustCompile, src)
		}
	}
	// Validate body uses pre-compiled var.
	if !strings.Contains(src, "_pattern0.MatchString") {
		t.Errorf("Validate() should reference precompiled var:\n%s", src)
	}
	mustParseGo(t, src)
}

func TestValidateMixinCascade(t *testing.T) {
	// Embedded mixins inherit field-promotion in Go but their own
	// Validate() doesn't fire automatically — without an explicit
	// call from the host, decorators declared on mixin fields never
	// validated.
	src := runValidateGen(t, `package design
type Audit { createdAt string @format(datetime) }
type User { Audit  id string }`)
	if !strings.Contains(src, "v.Audit.Validate()") {
		t.Errorf("mixin Validate cascade missing:\n%s", src)
	}
	mustParseGo(t, src)
}

func TestValidateErrorBody(t *testing.T) {
	// Error declarations with custom body fields must carry a
	// Validate() method just like regular types. Without it the
	// declared decorators on body fields became Go struct tags with
	// no runtime enforcement — clients could receive error payloads
	// that violate the design contract.
	src := runValidateGen(t, `package design
error Forbidden AccessDenied {
    reason   string @minLength(1) @maxLength(200)
    retryAfter int? @gte(1)
}
type X { id string }`)
	for _, want := range []string{
		"func (v *AccessDeniedBody) Validate() error",
		"len(v.Reason)",
		"v.RetryAfter != nil",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("error body validator missing %q:\n%s", want, src)
		}
	}
	mustParseGo(t, src)
}

func TestValidateMultiDimNestedArray(t *testing.T) {
	// C5: Node[][] previously emitted a single `for i := range v.Matrix`
	// that called Validate() on `v.Matrix[i]` — a []Node, not Node →
	// compile fail. Now must emit 2 nested loops.
	src := runValidateGen(t, `package design
type Node { id string }
type Catalog { matrix Node[][] }`)
	// Outer + inner loops, innermost body refs deepest element.
	for _, want := range []string{
		"for i0 := range v.Matrix",
		"for i1 := range v.Matrix[i0]",
		"v.Matrix[i0][i1].Validate()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("multi-dim nested validator missing %q:\n%s", want, src)
		}
	}
	mustParseGo(t, src)
}

func TestValidateMinMaxItemsOptionalArrayNilGuard(t *testing.T) {
	// C3: optional array (`T[]?`) must skip minItems/maxItems when nil.
	// `len(nil) == 0` would otherwise fail @minItems(1) even though
	// the field was marked optional.
	src := runValidateGen(t, `package design
type X { tags string[]? @minItems(1) @maxItems(5) }`)
	if !strings.Contains(src, "if v.Tags != nil {") {
		t.Errorf("optional array should be wrapped in nil-guard:\n%s", src)
	}
}

func TestValidateMinMaxItems(t *testing.T) {
	src := runValidateGen(t, `package design
type X { tags string[] @minItems(1) @maxItems(5) }`)
	for _, want := range []string{"len(v.Tags) < 1", "len(v.Tags) > 5"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidatePattern(t *testing.T) {
	src := runValidateGen(t, `package design
type X { code string @pattern("^[A-Z]+$") }`)
	if !strings.Contains(src, `regexp.MustCompile`) || !strings.Contains(src, `^[A-Z]+$`) {
		t.Errorf("pattern check missing:\n%s", src)
	}
}

func TestValidateFormatExpandedPatterns(t *testing.T) {
	// Each format name maps to its rendered error label. Some labels
	// match the @format() spelling (lowercase), others are formatted
	// for readability ("IPv6", "RFC 3339 datetime", "MAC address").
	cases := []struct {
		format string
		label  string
	}{
		{"ipv6", "IPv6"},
		{"datetime", "RFC 3339 datetime"},
		{"date", "date"},
		{"time", "time"},
		{"cidr", "CIDR"},
		{"mac", "MAC address"},
		{"creditcard", "credit card number"},
		{"base64", "base64"},
		{"hexcolor", "hex color"},
		{"json", "JSON"},
	}
	var fields []string
	for i, c := range cases {
		fields = append(fields, "f"+itoaSimple(i)+" string @format("+c.format+")")
	}
	src := runValidateGen(t, "package design\ntype X { "+strings.Join(fields, "  ")+" }")
	for _, c := range cases {
		want := "not a valid " + c.label
		if !strings.Contains(src, want) {
			t.Errorf("missing format %q (label %q) in generated validators:\n%s", c.format, c.label, src)
		}
	}
}

// ---------- cross-field validators ----------

func TestValidateRequiresOneOfNullableFields(t *testing.T) {
	// C4: @nullable forces pointer in Go. Cross-field validators must
	// emit `v.X == nil` for nullable fields, not value-shape compares
	// like `v.X == ""` (which fails to compile against `*string`).
	src := runValidateGen(t, `package design
@requiresOneOf(left, right)
type Choice {
    left  string @nullable
    right string @nullable
}`)
	if !strings.Contains(src, "v.Left == nil && v.Right == nil") {
		t.Errorf("nullable cross-field absence should be nil-check, got:\n%s", src)
	}
}

func TestValidateRequiresOneOf(t *testing.T) {
	src := runValidateGen(t, `package design
@requiresOneOf(["email", "phone"])
type Contact { email string?  phone string? }`)
	if !strings.Contains(src, "requiresOneOf") {
		t.Errorf("missing requiresOneOf message:\n%s", src)
	}
	// De Morgan'd absence-AND form (idiomatic for staticcheck QF1001).
	if !strings.Contains(src, "v.Email == nil && v.Phone == nil") {
		t.Errorf("expected absence-AND check (De Morgan'd):\n%s", src)
	}
	// Negative - the original `!(... || ...)` form must NOT leak in.
	if strings.Contains(src, "!(v.Email") {
		t.Errorf("non-De-Morgan'd form leaked:\n%s", src)
	}
}

func TestValidateMutuallyExclusive(t *testing.T) {
	src := runValidateGen(t, `package design
@mutuallyExclusive(["a", "b"])
type T { a bool  b bool }`)
	if !strings.Contains(src, "mutuallyExclusive") {
		t.Errorf("missing mutuallyExclusive message:\n%s", src)
	}
	if !strings.Contains(src, "n := 0") || !strings.Contains(src, "n > 1") {
		t.Errorf("expected counter-based check:\n%s", src)
	}
}

// ---------- enum value validation ----------

func TestValidateEnumValueSwitchEmitted(t *testing.T) {
	src := runValidateGen(t, `package design
enum Status { Active  Inactive  Pending }
type User { status Status }`)
	if !strings.Contains(src, "switch v.Status {") {
		t.Errorf("expected switch on enum field:\n%s", src)
	}
	if !strings.Contains(src, "case StatusActive, StatusInactive, StatusPending:") {
		t.Errorf("expected case list with enum constants:\n%s", src)
	}
	if !strings.Contains(src, "invalid Status value") {
		t.Errorf("expected enum error message:\n%s", src)
	}
}

func TestValidateEnumRequiredEnumAware(t *testing.T) {
	// String-base enum: compares against `""`, not `0`.
	src := runValidateGen(t, `package design
enum Color { Red  Green  Blue }
type Paint { c Color }`)
	if !strings.Contains(src, `v.C == ""`) {
		t.Errorf("expected string-empty check on string-base enum:\n%s", src)
	}
}

func TestValidateEnumIntRequiredZero(t *testing.T) {
	// Int-valued enum: compares against `0`.
	src := runValidateGen(t, `package design
enum Tier { Bronze = 1  Silver = 2 }
type Account { tier Tier }`)
	if !strings.Contains(src, "v.Tier == 0") {
		t.Errorf("expected int-zero check on int-base enum:\n%s", src)
	}
}

func TestValidateEnumArrayValidates(t *testing.T) {
	src := runValidateGen(t, `package design
enum Tag { A  B  C }
type Box { tags Tag[] }`)
	if !strings.Contains(src, "for i := range v.Tags {") {
		t.Errorf("expected loop on enum array:\n%s", src)
	}
	if !strings.Contains(src, "switch v.Tags[i] {") {
		t.Errorf("expected per-element switch:\n%s", src)
	}
}

func TestValidateEnumOptionalNilGuard(t *testing.T) {
	src := runValidateGen(t, `package design
enum Pri { Low  High }
type T { p Pri? }`)
	if !strings.Contains(src, "if v.P != nil {") {
		t.Errorf("expected nil-guard on optional enum:\n%s", src)
	}
	if !strings.Contains(src, "switch *v.P {") {
		t.Errorf("expected pointer-deref switch on optional enum:\n%s", src)
	}
}

func TestValidateEnumMultipleDecorators(t *testing.T) {
	// + @doc + @deprecated on the same enum field. Only
	// produces a check; @doc and @deprecated are metadata.
	// The auto enum-value check still appears alongside.
	src := runValidateGen(t, `package design
enum Sev { Low  High }
type Alert { level Sev @doc("severity") @deprecated }`)
	if !strings.Contains(src, `v.Level == ""`) {
		t.Errorf("expected check:\n%s", src)
	}
	if !strings.Contains(src, "switch v.Level {") {
		t.Errorf("expected auto enum-value switch:\n%s", src)
	}
	// @doc / @deprecated produce no runtime code - the body should
	// only have the two checks above (plus return nil).
	count := strings.Count(src, "return fmt.Errorf")
	if count != 2 {
		t.Errorf("expected exactly 2 error returns in Alert.Validate, got %d:\n%s", count, src)
	}
}

// ---------- generic types ----------

func TestValidateGenericReceiverEmitted(t *testing.T) {
	src := runValidateGen(t, `package design
type Page<T> { items T[]  total int }`)
	if !strings.Contains(src, "func (v *Page[T]) Validate() error") {
		t.Errorf("expected parametric receiver:\n%s", src)
	}
	// Generic-param-typed array → runtime type-assertion path.
	if !strings.Contains(src, "interface{ Validate() error }") {
		t.Errorf("expected runtime assertion path:\n%s", src)
	}
}

func TestValidateGenericPropagatesPrimitiveDecorators(t *testing.T) {
	// Numeric / array validators on non-generic-param fields should
	// still emit normally - only the type-param fields use the
	// runtime-assertion path.
	src := runValidateGen(t, `package design
type Page<T> {
    items   T[]    @minItems(1) @maxItems(50)
    total   int    @gte(0)
}`)
	for _, want := range []string{
		"len(v.Items) < 1",
		"len(v.Items) > 50",
		"v.Total < 0",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidateGenericInstanceCallsValidate(t *testing.T) {
	// A non-generic struct that embeds a generic instance (`Page[Book]`)
	// must call .Validate() on it directly. The generic decl now has a
	// Validate() method so the call type-checks at the concrete site.
	src := runValidateGen(t, `package design
type Book { id string }
type Page<T> { items T[] }
type BookList { p Page<Book> }`)
	if !strings.Contains(src, "v.P.Validate()") {
		t.Errorf("expected nested Validate() on generic instance:\n%s", src)
	}
}

func TestValidateMaxSize(t *testing.T) {
	src := runValidateGen(t, `package design
type Upload {
    avatar file @maxSize(5MB)
}`)
	// 5MB = 5 * 2^20 = 5242880
	if !strings.Contains(src, "v.Avatar.Size > 5242880") {
		t.Errorf("missing 5MB limit:\n%s", src)
	}
	if !strings.Contains(src, "v.Avatar != nil") {
		t.Errorf("missing nil guard:\n%s", src)
	}
}

func TestValidateMaxSizeAcceptsBareInt(t *testing.T) {
	src := runValidateGen(t, `package design
type Upload { avatar file @maxSize(1024) }`)
	if !strings.Contains(src, "v.Avatar.Size > 1024") {
		t.Errorf("bare int @maxSize not honoured:\n%s", src)
	}
}

func TestValidateMaxSizeRejectsNonFile(t *testing.T) {
	// @maxSize on a non-file field is rejected by the semantic
	// analyser (decorator/typemismatch). This used to be a silent
	// codegen-time skip; v1.x elevates it to a hard error so the IDE
	// can surface it before the user runs `craftgo gen`.
	p := craftparser.New("test.craftgo", `package design
type X { name string @maxSize(1024) }`)
	f := p.Parse()
	_, diags := semantic.Analyze([]*ast.File{f})
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, "@maxSize applies to file") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected typemismatch diag, got %v", diags)
	}
}

func TestValidateMimeTypes(t *testing.T) {
	src := runValidateGen(t, `package design
type Upload {
    avatar file @mimeTypes(["image/png", "image/jpeg"])
}`)
	for _, want := range []string{
		"v.Avatar != nil",
		`v.Avatar.Header.Get("Content-Type")`,
		`"image/png", "image/jpeg"`,
		"disallowed content type",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestValidateFileCombined(t *testing.T) {
	// Both decorators on the same file field should produce two checks.
	src := runValidateGen(t, `package design
type Upload {
    avatar file @maxSize(2MB) @mimeTypes(["image/png"])
}`)
	if !strings.Contains(src, "v.Avatar.Size > 2097152") {
		t.Errorf("missing maxSize check:\n%s", src)
	}
	if !strings.Contains(src, `"image/png"`) {
		t.Errorf("missing mimeTypes check:\n%s", src)
	}
}

// itoaSimple is a no-import substitute for strconv.Itoa so this test file
// stays free of the strconv import (every other generator test uses
// strings only).
func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	var sb strings.Builder
	if n < 0 {
		sb.WriteByte('-')
		n = -n
	}
	var stack []byte
	for n > 0 {
		stack = append(stack, byte('0'+n%10))
		n /= 10
	}
	for i := len(stack) - 1; i >= 0; i-- {
		sb.WriteByte(stack[i])
	}
	return sb.String()
}
