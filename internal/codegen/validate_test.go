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
    age   int @min(0)
    score int @max(100)
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
    age   int?     @min(0) @max(150)
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

func TestValidateMultipleOfSkipsFloat(t *testing.T) {
	// Float fields don't get a modulus check - `%` is integer-only.
	src := runValidateGen(t, `package design
type X { ratio float64 @multipleOf(2) }`)
	if strings.Contains(src, "%") && strings.Contains(src, "Ratio") {
		t.Errorf("float multipleOf should be skipped:\n%s", src)
	}
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
    total   int    @min(0)
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
