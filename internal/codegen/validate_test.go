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

func TestEnumCaseListDedupsCollidingMembers(t *testing.T) {
	// A case-colliding enum (Active / active both PascalCase to "Active")
	// emits deduped consts (StatusActive / StatusActive_2). The validate
	// case-list must use the SAME deduped names — a non-deduped walk emits
	// `case StatusActive, StatusActive`, a duplicate case that fails to
	// compile.
	src := runValidateGen(t, `package design
enum Status { Active  active }
type Req { s Status }`)
	if !strings.Contains(src, "StatusActive_2") {
		t.Errorf("validate case-list must reference the deduped const StatusActive_2:\n%s", src)
	}
	if strings.Contains(src, "case StatusActive, StatusActive:") {
		t.Errorf("validate case-list emitted a duplicate (non-deduped) case:\n%s", src)
	}
}

func TestRequiredStringEnumWithEmptyMember(t *testing.T) {
	// A required string-enum field whose enum defines "" as a real member
	// (`Unknown = ""`) must NOT emit a `== ""` presence check — "" is the Go
	// zero value, so the check would reject that legal member before the
	// value-set switch runs. Mirrors the int-0 guard.
	src := runValidateGen(t, `package design
enum Status { Unknown = ""  Active = "active" }
type Item { status Status }`)
	if strings.Contains(src, `v.Status == ""`) {
		t.Errorf("required check must not reject the empty-string enum member:\n%s", src)
	}
}

func TestRequiredStringEnumWithoutEmptyMember(t *testing.T) {
	// A string enum with no "" member still emits the presence check.
	src := runValidateGen(t, `package design
enum Color { Red = "red"  Blue = "blue" }
type Item { color Color }`)
	if !strings.Contains(src, `v.Color == ""`) {
		t.Errorf("required check should fire for a string enum with no empty member:\n%s", src)
	}
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
	mustContainAll(t, src,
		"len(v.A)",
	)
}

func TestValidateNumericBounds(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    age   int @gte(0)
    score int @lte(100)
    n     int @range(1, 99)
}`)
	mustContainAll(t, src,
		"v.Age < 0",
	)
}

func TestValidateNumericBoundsOptional(t *testing.T) {
	// `T?` numeric fields emit @min/@max/@range/@positive/@negative/
	// @multipleOf checks, nil-guarded so the deref runs only when a
	// value is present.
	src := runValidateGen(t, `package design
type X {
    age   int?     @gte(0) @lte(150)
    score int?     @range(0, 100)
    step  int?     @positive @multipleOf(5)
    delta float64? @negative
}`)
	mustContainAll(t, src,
		"v.Age != nil && *v.Age < 0",
		"v.Age != nil && *v.Age > 150",
		"v.Score != nil && (*v.Score < 0 || *v.Score > 100)",
		"v.Step != nil && *v.Step <= 0",
		"v.Step != nil && *v.Step%5 != 0",
		"v.Delta != nil && *v.Delta >= 0",
	)
}

func TestValidateFloatBounds(t *testing.T) {
	// Float bound literals (FloatLit) produce checks alongside integer
	// bounds (IntLit), so `@gte(0.5)` etc. emit a check.
	src := runValidateGen(t, `package design
type X {
    rate  float64 @gte(0.5) @lte(1.5)
    tax   float32 @range(0.0, 0.99)
    step  float64 @gt(0.1) @lt(0.9)
}`)
	mustContainAll(t, src,
		"v.Rate < 0.5",
		"v.Rate > 1.5",
		"v.Tax < 0 || v.Tax > 0.99",
		"v.Step <= 0.1",
		"v.Step >= 0.9",
	)
}

func TestValidateStrictBounds(t *testing.T) {
	// @gt and @lt are strict variants of @gte / @lte. Validity is
	// `x > N` / `x < N`; codegen emits the inverted form
	// `x <= N` / `x >= N` as the failure condition.
	src := runValidateGen(t, `package design
type X {
    pos  int @gt(0)
    bnd  int @lt(100)
    rng  int @gt(0) @lt(100)
}`)
	mustContainAll(t, src,
		"v.Pos <= 0",   // @gt(0) fails when x <= 0
		"v.Bnd >= 100", // @lt(100) fails when x >= 100
		"v.Rng <= 0",   // @gt(0) part of strict-both pair
		"v.Rng >= 100", // @lt(100) part of strict-both pair
		"must be greater than 0",
		"must be less than 100",
	)
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
	// Float fields with @multipleOf are rejected at semantic time
	// because Go's `%` operator is integer-only, keeping the runtime
	// validator consistent with the OpenAPI side that emits
	// `multipleOf: 0.5`.
	src := tryRunValidateGen(t, `package design
type X { ratio float64 @multipleOf(2) }`)
	if src != "" && strings.Contains(src, "v.Ratio%") {
		t.Errorf("float @multipleOf should be rejected, codegen emitted:\n%s", src)
	}
}

// tryRunValidateGen mirrors [runValidateGen] but returns "" instead
// of fatal-ing when the analyzer rejects the source. Used by negative
// tests that pin "this design is rejected" without bringing the test
// process down.
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
	// `map<K, V>` walks values when V is a user-defined type so the
	// inner Validate() (format/length/pattern decorators on V's
	// fields) actually runs per entry. Array and optional shapes of
	// V follow the same path — `map<string, User[]>` and
	// `map<string, User?>` each cascade through their per-element
	// or nil-guard wrapper.
	src := runValidateGen(t, `package design
type User { id string @minLength(1) }
type Catalog {
    plain   map<string, User>
    arrayV  map<string, User[]>
    optV    map<string, User?>
}`)
	mustContainAll(t, src,
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
	)
	mustParseGo(t, src)
}

func TestValidateRegexHoisted(t *testing.T) {
	// Patterns and regex-backed format catalogue entries compile
	// ONCE at package init via `var _pattern0 = regexp.MustCompile(...)`.
	// Inline compilation inside Validate() would pay the parser cost
	// on every call — unacceptable on the hot per-request path.
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
	// Validate() doesn't fire automatically — the host emits an
	// explicit call so decorators declared on mixin fields validate.
	src := runValidateGen(t, `package design
type Audit { createdAt string @format(datetime) }
type User { Audit  id string }`)
	if !strings.Contains(src, "v.Audit.Validate()") {
		t.Errorf("mixin Validate cascade missing:\n%s", src)
	}
	mustParseGo(t, src)
}

func TestValidateErrorBody(t *testing.T) {
	// Error declarations with custom body fields carry a Validate()
	// method just like regular types, so the declared decorators on
	// body fields are enforced at runtime rather than living only as
	// Go struct tags.
	src := runValidateGen(t, `package design
error Forbidden AccessDenied {
    reason   string @minLength(1) @maxLength(200)
    retryAfter int? @gte(1)
}
type X { id string }`)
	mustContainAll(t, src,
		"func (v *AccessDeniedBody) Validate() error",
		"len(v.Reason)",
		"v.RetryAfter != nil",
	)
	mustParseGo(t, src)
}

func TestValidateMultiDimNestedArray(t *testing.T) {
	// Multi-dim arrays of struct types need one for-loop per
	// dimension; the innermost body refs the deepest element. A
	// single loop would call Validate() on a slice (`[]Node`), not
	// on the struct.
	src := runValidateGen(t, `package design
type Node { id string }
type Catalog { matrix Node[][] }`)
	// Outer + inner loops, innermost body refs deepest element.
	mustContainAll(t, src,
		"for i0 := range v.Matrix",
		"for i1 := range v.Matrix[i0]",
		"v.Matrix[i0][i1].Validate()",
	)
	mustParseGo(t, src)
}

func TestValidateMinMaxItemsOptionalArrayNilGuard(t *testing.T) {
	// Optional arrays (`T[]?`) skip minItems/maxItems when nil:
	// `len(nil) == 0` would otherwise reject an absent field that
	// the `?` suffix explicitly allows.
	src := runValidateGen(t, `package design
type X { tags string[]? @minItems(1) @maxItems(5) }`)
	if !strings.Contains(src, "if v.Tags != nil {") {
		t.Errorf("optional array should be wrapped in nil-guard:\n%s", src)
	}
}

func TestValidateMinMaxItems(t *testing.T) {
	src := runValidateGen(t, `package design
type X { tags string[] @minItems(1) @maxItems(5) }`)
	mustContainAll(t, src,
		"len(v.Tags) < 1",
	)
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

// ---------- cross-package validators ----------

// TestValidateEmitsQualifiedGenericCall checks that a field typed
// `shared.Page<ProductRef>` emits a recursive validate call. The
// local-only `pkg.Types` lookup never matches the qualified name, so
// resolution routes through the project-wide TypeTable and the
// consuming Validate() body validates the cross-package generic's
// element.
func TestValidateEmitsQualifiedGenericCall(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
type Page<T> { items T[]  cursor string? }`,
		"app/types.craftgo": `package app
import "shared"
type ProductRef { id string }
type Product {
    id   string
    page shared.Page<ProductRef>
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	if appPkg == nil {
		t.Fatal("app package missing from project")
	}
	dir := t.TempDir()
	projTypes := BuildTypeTable(proj, "app")
	if err := GenerateValidatorsWith(appPkg, dir, nil, nil, projTypes); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, "v.Page.Validate()") {
		t.Errorf("expected `v.Page.Validate()` emitted for qualified generic field; got:\n%s", src)
	}
}

// projectFiles writes src to disk under a tempdir and returns the
// parsed []*ast.File. Mirrors the semantic.projectFixture helper but
// stays local to the codegen test package to avoid a cross-package
// test-helper import.
func projectFiles(t *testing.T, src map[string]string) (string, []*ast.File) {
	t.Helper()
	root := t.TempDir()
	var files []*ast.File
	for rel, content := range src {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		p := craftparser.New(full, content)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse %s: %v", rel, d)
		}
		files = append(files, f)
	}
	return root, files
}

// TestValidateEmitsCrossPkgEnumAllShapes covers every shape a
// cross-package enum field can take. The per-package `pkg.Enums`
// lookup never matches the qualified name, so resolution routes
// through the project-wide EnumTable and registers the cross-package
// import on the validate.go file, emitting the switch-case validity
// check for each shape.
func TestValidateEmitsCrossPkgEnumAllShapes(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/e.craftgo": `package shared
enum Color { Red  Green  Blue }`,
		"app/t.craftgo": `package app
import "shared"
type Pick {
    one     shared.Color
    many    shared.Color[]
    maybe   shared.Color?
    keyed   map<string, shared.Color>
    keyEnum map<shared.Color, string>
    both    map<shared.Color, shared.Color>
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{
		DesignRoot: root,
		// Disable manifest-driven middleware-ref validation so the
		// fixture stays minimal (no craftgo.design.yaml needed).
	})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	cross := CrossPkg{"shared": "github.com/test/m/internal/types/shared"}
	dir := t.TempDir()
	if err := GenerateValidatorsAll(appPkg, dir, cross, nil, nil, BuildEnumTable(proj, "app")); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	// Every shape dispatches through the enum's own Validate()
	// (generated in the shared package), so app's validate.go carries
	// the loop / nil-guard scaffolding plus a `.Validate()` call, not
	// an inlined switch. The value-set check lives once in shared, and
	// a generic instance over shared.Color picks it up the same way.
	mustContainAll(t, src,
		"if err := v.One.Validate(); err != nil",      // direct field
		"for i0 := range v.Many",                      // array
		"if err := v.Many[i0].Validate(); err != nil", //   per-element
		"if v.Maybe != nil",                           // optional
		"if err := v.Maybe.Validate(); err != nil",    //   inside guard
		"for _, val := range v.Keyed",                 // map value
		"for key := range v.KeyEnum",                  // map key
		"for key, val := range v.Both",                // map both
		"if err := key.Validate(); err != nil",
		"if err := val.Validate(); err != nil",
	)
	// The cross-package enum's constants and value-set switch live in
	// shared's validate.go, so app neither inlines the switch nor needs
	// the shared import (it calls a method on a value whose type is
	// already declared in app's types.go). gofmt -s simplification must
	// also not flag the map loops — CI's fmt-check runs `gofmt -l -s`
	// and any rewrite there would fail.
	mustContainNone(t, src,
		"switch v.One",
		"shared.ColorRed",
		"github.com/test/m/internal/types/shared",
		"for key, _ := range",
		"for _, _ := range",
	)
}

// TestValidateWalksMapKeyUserType covers a map keyed by a
// user-defined type (with its own Validate method): the loop emits a
// `for key := range m` walk and dispatches Validate on the key side
// as well as the value side.
func TestValidateWalksMapKeyUserType(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/t.craftgo": `package shared
type User { id string @length(1, 64) }`,
		"app/t.craftgo": `package app
import "shared"
type Bag { byUser map<shared.User, string> }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	dir := t.TempDir()
	if err := GenerateValidatorsAll(appPkg, dir, nil, nil, BuildTypeTable(proj, "app"), nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		"for key := range v.ByUser",
		"key.Validate()",
	)
}

// TestValidateOmitsCallWhenNoTypeTable covers the single-package
// fallback: callers that don't pass a TypeTable skip qualified refs,
// so no spurious compile error arises from a `.Validate()` call on a
// type the local package can't reach.
func TestValidateOmitsCallWhenNoTypeTable(t *testing.T) {
	pkg := analyze(t, `package app
type Product { id string }`)
	dir := t.TempDir()
	if err := GenerateValidators(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	// Sanity: no false-positive recursive call on a primitive field.
	if strings.Contains(src, "v.Id.Validate()") || strings.Contains(src, "v.ID.Validate()") {
		t.Errorf("primitive field must not get a recursive validate call:\n%s", src)
	}
}

// ---------- cross-field validators ----------

func TestValidateRequiresOneOfNullableFields(t *testing.T) {
	// @nullable forces a pointer in Go. Cross-field validators must
	// emit `v.X == nil` for nullable fields, not value-shape
	// comparisons like `v.X == ""` which fail to compile against
	// `*string`.
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
type T { a bool?  b bool? }`)
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
	// The value-set switch lives on the enum's OWN Validate() method
	// (`func (v Status) Validate()`), and the field dispatches through
	// it, keeping the check declared once across every use site and
	// letting generic instances over the enum validate too.
	if !strings.Contains(src, "func (v Status) Validate() error {") {
		t.Errorf("expected enum Validate() method:\n%s", src)
	}
	if !strings.Contains(src, "switch v {") {
		t.Errorf("expected switch on enum receiver:\n%s", src)
	}
	if !strings.Contains(src, "case StatusActive, StatusInactive, StatusPending:") {
		t.Errorf("expected case list with enum constants:\n%s", src)
	}
	if !strings.Contains(src, "invalid Status value") {
		t.Errorf("expected enum error message:\n%s", src)
	}
	if !strings.Contains(src, "if err := v.Status.Validate(); err != nil {") {
		t.Errorf("expected enum field to dispatch through Validate():\n%s", src)
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
	// Array-of-enum loops and dispatches each element through the
	// enum's Validate(); the value-set switch lives on Tag.Validate().
	if !strings.Contains(src, "for i0 := range v.Tags {") {
		t.Errorf("expected loop on enum array:\n%s", src)
	}
	if !strings.Contains(src, "if err := v.Tags[i0].Validate(); err != nil {") {
		t.Errorf("expected per-element Validate() dispatch:\n%s", src)
	}
	if !strings.Contains(src, "func (v Tag) Validate() error {") {
		t.Errorf("expected enum Validate() method carrying the switch:\n%s", src)
	}
}

func TestValidateEnumOptionalNilGuard(t *testing.T) {
	src := runValidateGen(t, `package design
enum Pri { Low  High }
type T { p Pri? }`)
	// Optional enum: nil-guard then dispatch. The value method has a
	// value receiver, so calling it on the *Pri pointer auto-derefs —
	// no explicit `*v.P` deref is emitted in the host.
	if !strings.Contains(src, "if v.P != nil {") {
		t.Errorf("expected nil-guard on optional enum:\n%s", src)
	}
	if !strings.Contains(src, "if err := v.P.Validate(); err != nil {") {
		t.Errorf("expected Validate() dispatch inside the nil-guard:\n%s", src)
	}
	if strings.Contains(src, "switch *v.P {") {
		t.Errorf("did not expect an inline pointer-deref switch in the host:\n%s", src)
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
		t.Errorf("expected required-presence check:\n%s", src)
	}
	// The auto value-set switch lives on Sev.Validate(); the field
	// dispatches through it.
	if !strings.Contains(src, "switch v {") {
		t.Errorf("expected auto enum-value switch on the enum receiver:\n%s", src)
	}
	if !strings.Contains(src, "if err := v.Level.Validate(); err != nil {") {
		t.Errorf("expected enum field to dispatch through Validate():\n%s", src)
	}
	// @doc / @deprecated produce no runtime code. The file holds
	// exactly two error returns: the required-presence check in
	// Alert.Validate and the value-set rejection in Sev.Validate.
	count := strings.Count(src, "return fmt.Errorf")
	if count != 2 {
		t.Errorf("expected exactly 2 error returns total, got %d:\n%s", count, src)
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
	mustContainAll(t, src,
		"len(v.Items) < 1",
		"len(v.Items) > 50",
		"v.Total < 0",
	)
}

func TestValidateGenericInstanceCallsValidate(t *testing.T) {
	// A non-generic struct that embeds a generic instance (`Page[Book]`)
	// calls .Validate() on it directly. The generic decl has a
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
	// analyser (decorator/typemismatch). The check fires at semantic
	// time so the IDE surfaces it before the user runs `craftgo gen`.
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
	mustContainAll(t, src,
		"v.Avatar != nil",
		`v.Avatar.Header.Get("Content-Type")`,
		`"image/png", "image/jpeg"`,
		"disallowed content type",
	)
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

// TestValidateMapItemsBound covers @minItems/@maxItems on a map: they
// emit a runtime len() entry-count check.
func TestValidateMapItemsBound(t *testing.T) {
	src := runValidateGen(t, `package design
type X { counts map<string, int> @minItems(1) @maxItems(10) }`)
	mustContainAll(t, src, "len(v.Counts) < 1", "len(v.Counts) > 10")
}

// TestValidateNullableNestedNilGuarded covers a @nullable nested
// struct / enum / generic-instance field: it lowers to a Go pointer,
// so Validate() nil-guards it — decoding JSON null (or omitting the
// field) would otherwise nil-deref and PANIC the handler.
func TestValidateNullableNestedNilGuarded(t *testing.T) {
	src := runValidateGen(t, `package design
type Inner { name string @minLength(1) }
enum Color { Red  Green  Blue }
type Page<T> { items T[]  total int }
type Host {
    sNull Inner @nullable
    eNull Color @nullable
    gNull Page<Inner> @nullable
}`)
	mustContainAll(t, src,
		"if v.SNull != nil {",
		"if v.GNull != nil {",
		"if v.ENull != nil {",
		// Enum dispatches through its value-receiver Validate() inside
		// the nil-guard; the deref is implicit, no inline switch.
		"if err := v.ENull.Validate(); err != nil {",
	)
	if strings.Contains(src, "switch *v.ENull {") {
		t.Errorf("did not expect an inline pointer-deref switch for @nullable enum:\n%s", src)
	}
}
