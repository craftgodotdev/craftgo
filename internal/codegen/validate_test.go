package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	src := runValidateGen(t, `package design
type X { name string @required }`)
	if !strings.Contains(src, `v.Name == ""`) {
		t.Errorf("missing required check:\n%s", src)
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
	// Float fields don't get a modulus check — `%` is integer-only.
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
	// Each new format name should produce a regex check. We pick one
	// representative each so the test stays compact while still covering
	// every newly added catalogue entry.
	formats := []string{
		"ipv6", "datetime", "date", "time", "cidr", "mac",
		"creditcard", "base64", "hexcolor", "json",
	}
	var fields []string
	for i, fmt := range formats {
		fields = append(fields, "f"+itoaSimple(i)+" string @format("+fmt+")")
	}
	src := runValidateGen(t, "package design\ntype X { "+strings.Join(fields, "  ")+" }")
	for _, fmt := range formats {
		if !strings.Contains(src, "not a valid "+fmt) {
			t.Errorf("missing format %q in generated validators:\n%s", fmt, src)
		}
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
	// String-base enum: @required compares against `""`, not `0`.
	src := runValidateGen(t, `package design
enum Color { Red  Green  Blue }
type Paint { c Color @required }`)
	if !strings.Contains(src, `v.C == ""`) {
		t.Errorf("expected string-empty check on string-base enum:\n%s", src)
	}
}

func TestValidateEnumIntRequiredZero(t *testing.T) {
	// Int-valued enum: @required compares against `0`.
	src := runValidateGen(t, `package design
enum Tier { Bronze = 1  Silver = 2 }
type Account { tier Tier @required }`)
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
	// @required + @doc + @deprecated on the same enum field. Only
	// @required produces a check; @doc and @deprecated are metadata.
	// The auto enum-value check still appears alongside.
	src := runValidateGen(t, `package design
enum Sev { Low  High }
type Alert { level Sev @required @doc("severity") @deprecated }`)
	if !strings.Contains(src, `v.Level == ""`) {
		t.Errorf("expected @required check:\n%s", src)
	}
	if !strings.Contains(src, "switch v.Level {") {
		t.Errorf("expected auto enum-value switch:\n%s", src)
	}
	// @doc / @deprecated produce no runtime code — the body should
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
	// still emit normally — only the type-param fields use the
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
type Book { id string @required }
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
	// @maxSize on a string field is a no-op (silently skipped).
	src := runValidateGen(t, `package design
type X { name string @maxSize(1024) }`)
	if strings.Contains(src, ".Size") {
		t.Errorf("@maxSize on non-file should be skipped:\n%s", src)
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
