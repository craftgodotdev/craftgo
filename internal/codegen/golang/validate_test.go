package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// runValidateGen returns the validate.go generated for src.
func runValidateGen(t *testing.T, src string) string {
	t.Helper()
	pkg := analyze(t, src)
	dir := t.TempDir()
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "validate.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	return string(out)
}

// The enum case list names the deduplicated consts of case-colliding members.
func TestEnumCaseListDedupsCollidingMembers(t *testing.T) {
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

// A required string enum with a "" member gets no == "" presence check.
func TestRequiredStringEnumWithEmptyMember(t *testing.T) {
	src := runValidateGen(t, `package design
enum Status { Unknown = ""  Active = "active" }
type Item { status Status }`)
	if strings.Contains(src, `v.Status == ""`) {
		t.Errorf("required check must not reject the empty-string enum member:\n%s", src)
	}
}

// A required string enum without a "" member gets the == "" presence check.
func TestRequiredStringEnumWithoutEmptyMember(t *testing.T) {
	src := runValidateGen(t, `package design
enum Color { Red = "red"  Blue = "blue" }
type Item { color Color }`)
	if !strings.Contains(src, `v.Color == ""`) {
		t.Errorf("required check should fire for a string enum with no empty member:\n%s", src)
	}
}

// A required string gets no presence check (null decodes to ""); a required any gets == nil.
func TestValidateRequired(t *testing.T) {
	src := runValidateGen(t, `package design
type X { name string }`)
	if strings.Contains(src, `v.Name == ""`) {
		t.Errorf("required-by-default must not emit an empty-string check on plain string:\n%s", src)
	}
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
	// Length counts runes, as OpenAPI minLength and maxLength do.
	mustContainAll(t, src,
		"utf8.RuneCountInString(v.A)",
		"utf8.RuneCountInString(v.B) < 1",
		"utf8.RuneCountInString(v.C) > 50",
	)
}

// A length is counted once: an exact one compares with !=, an optional range counts inside
// its nil guard; a zero minimum, which no length fails, emits no check.
func TestValidateLengthShapes(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    exact    string  @length(3)
    optExact string? @length(3)
    span     string? @length(2, 5)
    zeroLow  string  @length(0, 9)
    zeroMin  string  @minLength(0)
    bin      bytes?  @length(4, 8)
}`)
	mustContainAll(t, src,
		"if utf8.RuneCountInString(v.Exact) != 3 {",
		"if v.OptExact != nil && utf8.RuneCountInString(*v.OptExact) != 3 {",
		"if v.Span != nil {\n\t\tif l := utf8.RuneCountInString(*v.Span); l < 2 || l > 5 {",
		"if utf8.RuneCountInString(v.ZeroLow) > 9 {",
		"if v.Bin != nil {\n\t\tif l := len(v.Bin); l < 4 || l > 8 {",
	)
	mustContainNone(t, src, "v.ZeroMin", "< 0")
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

// Numeric constraints on an optional field are nil-guarded.
func TestValidateNumericBoundsOptional(t *testing.T) {
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

// Float bounds emit checks like integer ones.
func TestValidateFloatBounds(t *testing.T) {
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

// @gt and @lt are strict, so the bound itself fails.
func TestValidateStrictBounds(t *testing.T) {
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

// A whole float divisor is checked as the integer it holds, past int64 too.
func TestValidateMultipleOfWholeFloat(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    count int    @multipleOf(5.0)
    big   uint64 @multipleOf(10000000000000000000.0)
}`)
	for _, want := range []string{"v.Count%5 != 0", "v.Big%10000000000000000000 != 0", "must be a multiple of 10000000000000000000"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

// A whole float bound at an integer primitive's limit is emitted as the exact integer it writes.
func TestValidateIntegerLimitFloatBounds(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    u  uint64 @multipleOf(18446744073709551615.0)
    i  int64  @multipleOf(9223372036854775807.0)
    lo int64  @lte(9223372036854775807.0)
    r  int64  @range(-9223372036854775808.0, 9223372036854775807.0)
}`)
	mustContainAll(t, src,
		"v.U%18446744073709551615 != 0",
		"v.I%9223372036854775807 != 0",
		"v.Lo > 9223372036854775807",
		"v.R < -9223372036854775808 || v.R > 9223372036854775807",
	)
}

// @multipleOf on a float field is rejected, since Go's % is integer-only.
func TestValidateMultipleOfRejectsFloat(t *testing.T) {
	src := tryRunValidateGen(t, `package design
type X { ratio float64 @multipleOf(2) }`)
	if src != "" && strings.Contains(src, "v.Ratio%") {
		t.Errorf("float @multipleOf should be rejected, codegen emitted:\n%s", src)
	}
}

// tryRunValidateGen returns the generated validate.go, or "" on any diagnostic or failure.
func tryRunValidateGen(t *testing.T, src string) string {
	t.Helper()
	pkg, diags := semantic.Analyze([]*ast.File{mustParse(t, src)})
	if len(diags) > 0 {
		return ""
	}
	dir := t.TempDir()
	if err := generateValidators(pkg, dir, nil); err != nil {
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

// A map of a user type validates each value, through array and optional value shapes too.
func TestValidateMapStructValueRecurses(t *testing.T) {
	src := runValidateGen(t, `package design
type User { id string @minLength(1) }
type Catalog {
    plain   map<string, User>
    arrayV  map<string, User[]>
    optV    map<string, User?>
}`)
	mustContainAll(t, src,
		// plain: range values, validate each
		"for _, val0 := range v.Plain",
		"val0.Validate()",
		// array value: outer loop + inner loop
		"for _, val0 := range v.ArrayV",
		"for i1 := range val0",
		"val0[i1].Validate()",
		// optional value: range + nil-guard
		"for _, val0 := range v.OptV",
		"if val0 != nil",
	)
	mustParseGo(t, src)
}

// A message names a field as the request reads it: an auto-bound query or path parameter
// by its parameter name, a field any request reads from JSON by its JSON key.
func TestValidateNamesTheBoundParameter(t *testing.T) {
	src := runValidateGen(t, `package design
scalar Code string
type Page { pageSize int @json("page_size") @gte(1) }
type ListReq { Page  limit int @json("max") @lte(9) }
type PathReq { id string @json("ident") @minLength(2) }
type BodyReq { size int @json("the_size") @gte(1)  code Code @json("c") @maxLength(5) }
type Both { n int @json("n_json") @gte(1) }
type Resp { ok bool }
service S {
    get  List /items    { request ListReq  response Resp }
    get  One  /one/{id} { request PathReq  response Resp }
    post Make /items    { request BodyReq  response Resp }
    get  Read /both     { request Both     response Resp }
    post Save /both     { request Both     response Resp }
}`)
	mustContainAll(t, src,
		`"pageSize: below minimum 1"`,
		`"limit: above maximum 9"`,
		`"id: length less than 2"`,
		`"the_size: below minimum 1"`,
		`"c: length greater than 5"`,
		`"n_json: below minimum 1"`,
	)
}

// A map's key and value errors name the field's JSON key alike, at any nesting.
func TestValidateMapKeyAndValueNameOneSubject(t *testing.T) {
	src := runValidateGen(t, `package design
scalar Code string @minLength(2)
scalar Email string @format(email)
type Holder {
    byCode map<Code, Email> @json("by_code")
    nested map<string, map<Code, Email>> @json("nested_map")
}`)
	for _, subject := range []string{"by_code", "nested_map"} {
		if got := strings.Count(src, `fmt.Errorf("`+subject+`: %w", err)`); got != 2 {
			t.Errorf("want the key and the value error to name %q, got %d:\n%s", subject, got, src)
		}
	}
	mustContainNone(t, src, `"byCode: %w"`, `"nested: %w"`)
}

// Patterns and regex-backed formats compile once into deduplicated package-level vars.
func TestValidateRegexHoisted(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    code   string @pattern("^[A-Z]+$")
    sku    string @pattern("^[A-Z]+$")
    uuidV  string @format(uuid)
    color  string @format(hexcolor)
}`)
	if !strings.Contains(src, "var (") || !strings.Contains(src, "= regexp.MustCompile(") {
		t.Errorf("expected package-level regex var block:\n%s", src)
	}
	if strings.Contains(src, "regexp.MustCompile(") {
		nMustCompile := strings.Count(src, "regexp.MustCompile(")
		// The two identical @pattern regexes share one var.
		if nMustCompile != 3 {
			t.Errorf("expected 3 unique regex vars (^[A-Z]+$ deduped, uuid, hexcolor), got %d:\n%s", nMustCompile, src)
		}
	}
	if !strings.Contains(src, "_pattern0.MatchString") {
		t.Errorf("Validate() should reference precompiled var:\n%s", src)
	}
	mustParseGo(t, src)
}

// A type's Validate calls its embedded mixin's Validate.
func TestValidateMixinCascade(t *testing.T) {
	src := runValidateGen(t, `package design
type Audit { createdAt string @format(datetime) }
type User { Audit  id string }`)
	if !strings.Contains(src, "v.Audit.Validate()") {
		t.Errorf("mixin Validate cascade missing:\n%s", src)
	}
	mustParseGo(t, src)
}

// An error body gets a Validate method enforcing its field decorators.
func TestValidateErrorBody(t *testing.T) {
	src := runValidateGen(t, `package design
error Forbidden AccessDenied {
    reason   string @minLength(1) @maxLength(200)
    retryAfter int? @gte(1)
}
type X { id string }`)
	mustContainAll(t, src,
		"func (v *AccessDeniedBody) Validate() error",
		"utf8.RuneCountInString(v.Reason)",
		"v.RetryAfter != nil",
	)
	mustParseGo(t, src)
}

// A multi-dimensional struct array gets one loop per dimension around the element's Validate.
func TestValidateMultiDimNestedArray(t *testing.T) {
	src := runValidateGen(t, `package design
type Node { id string }
type Catalog { matrix Node[][] }`)
	mustContainAll(t, src,
		"for i0 := range v.Matrix",
		"for i1 := range v.Matrix[i0]",
		"v.Matrix[i0][i1].Validate()",
	)
	mustParseGo(t, src)
}

// An optional array skips @minItems/@maxItems when nil.
func TestValidateMinMaxItemsOptionalArrayNilGuard(t *testing.T) {
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

// Each @format names its readable label in the error message.
func TestValidateFormatExpandedPatterns(t *testing.T) {
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

// A field of a cross-package generic instance calls the instance's Validate.
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
	if err := generateValidators(appPkg, dir, &projectResolver{Resolver: semantic.NewResolver(proj, "app")}); err != nil {
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

// projectFiles writes src under a temp root and returns the root and the parsed files.
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

// Every shape of a cross-package enum field calls the enum's own Validate.
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
	})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	cross := crossPkg{"shared": "github.com/test/m/internal/types/shared"}
	dir := t.TempDir()
	if err := generateValidators(appPkg, dir, &projectResolver{Resolver: semantic.NewResolver(proj, "app"), CrossPkg: cross}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		"if err := v.One.Validate(); err != nil",      // direct field
		"for i0 := range v.Many",                      // array
		"if err := v.Many[i0].Validate(); err != nil", //   per-element
		"if v.Maybe != nil",                           // optional
		"if err := v.Maybe.Validate(); err != nil",    //   inside guard
		"for _, val0 := range v.Keyed",                // map value
		"for key0 := range v.KeyEnum",                 // map key
		"for key0, val0 := range v.Both",              // map both
		"if err := key0.Validate(); err != nil",
		"if err := val0.Validate(); err != nil",
	)
	// app neither inlines the switch nor imports shared, and its loops need no gofmt -s rewrite.
	mustContainNone(t, src,
		"switch v.One",
		"shared.ColorRed",
		"github.com/test/m/internal/types/shared",
		"for key0, _ := range",
		"for _, _ := range",
	)
}

// A map keyed by a cross-package scalar walks its keys and calls key.Validate().
func TestValidateWalksMapKeyUserType(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/t.craftgo": `package shared
scalar Email string @format(email) @length(1, 64)`,
		"app/t.craftgo": `package app
import "shared"
type Bag { byEmail map<shared.Email, string> }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	dir := t.TempDir()
	if err := generateValidators(appPkg, dir, &projectResolver{Resolver: semantic.NewResolver(proj, "app")}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		"for key0 := range v.ByEmail",
		"key0.Validate()",
	)
}

// Without a resolver, a primitive field gets no nested Validate call.
func TestValidateOmitsCallWhenNoTypeTable(t *testing.T) {
	pkg := analyze(t, `package app
type Product { id string }`)
	dir := t.TempDir()
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	if strings.Contains(src, "v.Id.Validate()") || strings.Contains(src, "v.ID.Validate()") {
		t.Errorf("primitive field must not get a recursive validate call:\n%s", src)
	}
}

// ---------- cross-field validators ----------

// A @nullable member of a cross-field group is presence-checked with == nil.
func TestValidateRequiresOneOfNullableFields(t *testing.T) {
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
	if strings.Contains(src, "!(v.Email") {
		t.Errorf("non-De-Morgan'd form leaked:\n%s", src)
	}
}

// A raw cross-field member is absent only when nil: an explicit null is the four bytes null.
func TestValidateCrossFieldOnRawBytes(t *testing.T) {
	src := runValidateGen(t, `package design
@requiresOneOf(left, right)
type Choice {
    left  bytes? @format(raw)
    right bytes? @format(raw)
}`)
	if !strings.Contains(src, "v.Left == nil && v.Right == nil") {
		t.Errorf("a raw cross-field absence should be a nil check, got:\n%s", src)
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

// An enum's value-set switch lives on its own Validate, which its fields call.
func TestValidateEnumValueSwitchEmitted(t *testing.T) {
	src := runValidateGen(t, `package design
enum Status { Active  Inactive  Pending }
type User { status Status }`)
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

// A required string-based enum field is checked against "".
func TestValidateEnumRequiredEnumAware(t *testing.T) {
	src := runValidateGen(t, `package design
enum Color { Red  Green  Blue }
type Paint { c Color }`)
	if !strings.Contains(src, `v.C == ""`) {
		t.Errorf("expected string-empty check on string-base enum:\n%s", src)
	}
}

// A required int-based enum field is checked against 0.
func TestValidateEnumIntRequiredZero(t *testing.T) {
	src := runValidateGen(t, `package design
enum Tier { Bronze = 1  Silver = 2 }
type Account { tier Tier }`)
	if !strings.Contains(src, "v.Tier == 0") {
		t.Errorf("expected int-zero check on int-base enum:\n%s", src)
	}
}

// An enum array validates each element through the enum's Validate.
func TestValidateEnumArrayValidates(t *testing.T) {
	src := runValidateGen(t, `package design
enum Tag { A  B  C }
type Box { tags Tag[] }`)
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

// An optional enum is nil-guarded, then calls its value-receiver Validate through the pointer.
func TestValidateEnumOptionalNilGuard(t *testing.T) {
	src := runValidateGen(t, `package design
enum Pri { Low  High }
type T { p Pri? }`)
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

// @doc and @deprecated on an enum field add no runtime code.
func TestValidateEnumMultipleDecorators(t *testing.T) {
	src := runValidateGen(t, `package design
enum Sev { Low  High }
type Alert { level Sev @doc("severity") @deprecated }`)
	if !strings.Contains(src, `v.Level == ""`) {
		t.Errorf("expected required-presence check:\n%s", src)
	}
	if !strings.Contains(src, "switch v {") {
		t.Errorf("expected auto enum-value switch on the enum receiver:\n%s", src)
	}
	if !strings.Contains(src, "if err := v.Level.Validate(); err != nil {") {
		t.Errorf("expected enum field to dispatch through Validate():\n%s", src)
	}
	// The field wraps the enum's subject-less message with its own name.
	if !strings.Contains(src, `return fmt.Errorf("level: %w", err)`) {
		t.Errorf("expected enum field error wrapped with the field name:\n%s", src)
	}
	if !strings.Contains(src, `"invalid Sev value"`) || strings.Contains(src, `"Sev: invalid Sev value"`) {
		t.Errorf("enum value-set message should be subject-less:\n%s", src)
	}
	// Alert returns from its presence check and its wrap, Sev from its value-set check.
	count := strings.Count(src, "return fmt.Errorf")
	if count != 3 {
		t.Errorf("expected exactly 3 error returns total, got %d:\n%s", count, src)
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

// A type parameter's value is probed at run time wherever it sits: through an optional's
// pointer, in every array dimension and as a map value.
func TestValidateTypeParamProbes(t *testing.T) {
	src := runValidateGen(t, `package design
type Box<T> {
    one  T
    opt  T?
    many T[]
    grid T[][]
    byId map<string, T>
}`)
	mustContainAll(t, src,
		"any(&v.One).(interface{ Validate() error })",
		"if v.Opt != nil {",
		"any(v.Opt).(interface{ Validate() error })",
		"for i0 := range v.Many {",
		"any(&v.Many[i0]).(interface{ Validate() error })",
		"for i1 := range v.Grid[i0] {",
		"any(&v.Grid[i0][i1]).(interface{ Validate() error })",
		"for _, val0 := range v.ByID {",
		"any(&val0).(interface{ Validate() error })",
	)
}

// Constraint decorators on a generic type's fields emit their usual checks.
func TestValidateGenericPropagatesPrimitiveDecorators(t *testing.T) {
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

// A field of a generic instance calls the instance's Validate.
func TestValidateGenericInstanceCallsValidate(t *testing.T) {
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

// @maxSize on a non-file field is a semantic error.
func TestValidateMaxSizeRejectsNonFile(t *testing.T) {
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

// @maxSize and @mimeTypes on one file field emit both checks.
func TestValidateFileCombined(t *testing.T) {
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

// itoaSimple returns n in decimal.
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

// @minItems and @maxItems on a map check its entry count.
func TestValidateMapItemsBound(t *testing.T) {
	src := runValidateGen(t, `package design
type X { counts map<string, int> @minItems(1) @maxItems(10) }`)
	mustContainAll(t, src, "len(v.Counts) < 1", "len(v.Counts) > 10")
}

// A @nullable struct, enum or generic-instance field is a pointer that Validate nil-guards.
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
		// The enum's value-receiver Validate is called through the pointer.
		"if err := v.ENull.Validate(); err != nil {",
	)
	if strings.Contains(src, "switch *v.ENull {") {
		t.Errorf("did not expect an inline pointer-deref switch for @nullable enum:\n%s", src)
	}
}

// @uniqueItems over a cross-package element imports that package for its dedupe map.
func TestUniqueItemsCrossPkgElementImport(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Name string @minLength(1)`,
		"app/t.craftgo": `package app
import "shared"
type U { names shared.Name[] @uniqueItems }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	cross := crossPkg{"shared": "github.com/test/m/internal/types/shared"}
	dir := t.TempDir()
	if err := generateValidators(proj.Packages["app"], dir, &projectResolver{Resolver: semantic.NewResolver(proj, "app"), CrossPkg: cross}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "app", "validate.go"))
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, "internal/types/shared") {
		t.Errorf("@uniqueItems over shared.Name must import shared; got:\n%s", src)
	}
}

// A generic instance over a scalar reaches the scalar's Validate through the runtime assertion.
func TestValidateGenericScalarArg(t *testing.T) {
	src := runValidateGen(t, `package design
scalar Email string @format(email)
type Page<T> { items T[] }
type EmailList { p Page<Email> }`)
	mustContainAll(t, src, "v.P.Validate()", "interface{ Validate() error }")
}

// A scalar over bytes is rejected in a cross-field group: its absence is emptiness, not nil.
func TestScalarOverBytesRejectedInCrossFieldGroup(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
scalar Blob bytes
@requiresOneOf(a, b)
type Pick {
  a Blob?
  b string?
}`,
	})
	_, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic rejecting the scalar-over-bytes cross-field member")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, "present/absent") || strings.Contains(d.Msg, "always treated as present") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a present/absent rejection, got: %v", diags)
	}
}

// An optional scalar over int is a pointer, so it is a valid cross-field member.
func TestScalarOverValueCrossFieldClean(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
scalar Cents int
@requiresOneOf(a, b)
type Pick {
  a Cents?
  b string?
}`,
	})
	_, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	for _, d := range diags {
		if strings.Contains(d.Msg, "present/absent") {
			t.Fatalf("scalar-over-int cross-field member wrongly rejected: %v", d)
		}
	}
}

// A required any[] field gets no nil presence check, like any other required slice.
func TestRequiredAnyArrayNoPresenceCheck(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
type Body { reqStrArr string[]  reqAnyArr any[] }
type Resp { ok bool }
service S { post Op /x { request Body  response Resp } }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	dir := t.TempDir()
	mPkg := proj.Packages["m"]
	if err := generateValidators(mPkg, dir, &projectResolver{Resolver: semantic.NewResolver(proj, "m")}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "m", "validate.go"))
	if strings.Contains(string(out), "ReqAnyArr == nil") {
		t.Errorf("any[] field wrongly got a nil presence check:\n%s", out)
	}
}

// A file member of a cross-field group is a *multipart.FileHeader, checked with == nil.
func TestCrossFieldFileMemberPresenceIsNilCheck(t *testing.T) {
	pkg := analyze(t, `package design
@requiresOneOf(doc, link)
type Attach {
  doc  file?
  link string?
}`)
	dir := t.TempDir()
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "validate.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "v.Doc == nil") {
		t.Fatalf("expected a nil presence check for the file member, got:\n%s", out)
	}
}
