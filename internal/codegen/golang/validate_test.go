package golang

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

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

// A whole float bound near an integer primitive's limit is emitted as the exact integer it writes.
func TestValidateIntegerLimitFloatBounds(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    u  uint64 @multipleOf(18446744073709551615.0)
    i  int64  @multipleOf(9223372036854775807.0)
    lo int64  @lte(9223372036854775806.0)
    r  int64  @range(-9223372036854775807.0, 9223372036854775806.0)
}`)
	mustContainAll(t, src,
		"v.U%18446744073709551615 != 0",
		"v.I%9223372036854775807 != 0",
		"v.Lo > 9223372036854775806",
		"v.R < -9223372036854775807 || v.R > 9223372036854775806",
	)
}

// A bound the Go type already enforces gets no check: an unsigned value's 0
// floor, an integer's own range, a count's 0 floor and MaxInt64 ceiling; a
// @range or @length keeps the end that bites.
func TestValidateSkipsBoundsTheTypeEnforces(t *testing.T) {
	src := runValidateGen(t, `package design
type X {
    a uint   @gte(0) @lte(1000)
    b uint   @range(0, 10)
    c uint8  @range(0, 255)
    d int8   @gte(-128) @lte(127)
    e int32  @gte(0) @lte(2147483647)
    f int64  @range(-9223372036854775808.0, 9223372036854775807.0)
    g string @minLength(0) @maxLength(9223372036854775807)
    h int[]  @minItems(0) @maxItems(9223372036854775807)
    i string @length(3, 9223372036854775807)
    j uint16 @gt(0) @lt(65535)
}`)
	mustContainAll(t, src,
		"if v.A > 1000 {",
		"if v.B > 10 {",
		"if v.E < 0 {",
		"if utf8.RuneCountInString(v.I) < 3 {",
		"if v.J <= 0 {",
		"if v.J >= 65535 {",
	)
	mustContainNone(t, src,
		"v.A < 0", "v.B < 0", "v.C ", "v.D ", "v.E > ", "v.F ", "v.G)", "len(v.H)", "> 9223372036854775807")
}

// A float bound keeps its check at the type's edge: a float parameter can
// carry NaN or an infinity.
func TestValidateKeepsFloatBoundsAtTheEdge(t *testing.T) {
	src := runValidateGen(t, `package design
type X { x float32 @gte(-340282346638528859811704183484516925440.0) @lte(340282346638528859811704183484516925440.0) }`)
	mustContainAll(t, src, "if v.X < -3.4028234663852886e+38 {", "if v.X > 3.4028234663852886e+38 {")
}

// A scalar whose every check is a bound its type enforces keeps an empty
// Validate(), which no field calls.
func TestValidateSkipsScalarWhoseChecksTheTypeEnforces(t *testing.T) {
	src := runValidateGen(t, `package design
scalar Byte uint8 @gte(0) @lte(255)
type X {
    b  Byte
    bs Byte[]
    m  map<string, Byte>
    p  Byte?
}`)
	mustContainAll(t, src, "func (v Byte) Validate() error {\n\treturn nil\n}")
	mustContainNone(t, src, "Validate(); err != nil", "for ")
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

// A nested value's error names the field that holds it, a struct's too, so a
// failure deep in a body reads as a path; a mixin's fields are the type's
// own and stay unprefixed.
func TestValidateNestedErrorsNameTheirField(t *testing.T) {
	src := runValidateGen(t, `package design
@requiresOneOf(email, phone)
type Contact { email string?  phone string? }
type Audit { by string @minLength(1) }
type Page<T> { items T[] }
type Order {
    Audit
    contact Contact
    backups Contact[]
    byName  map<string, Contact>
    boss    Order?
    page    Page<Contact>
}`)
	mustContainAll(t, src,
		"if err := v.Contact.Validate(); err != nil {\n\t\treturn fmt.Errorf(\"contact: %w\", err)",
		`return fmt.Errorf("backups: %w", err)`,
		`return fmt.Errorf("byName: %w", err)`,
		`return fmt.Errorf("boss: %w", err)`,
		`return fmt.Errorf("page: %w", err)`,
		`return fmt.Errorf("items: %w", err)`,
		"if err := v.Audit.Validate(); err != nil {\n\t\treturn err\n\t}",
	)
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
		fields = append(fields, "f"+strconv.Itoa(i)+" string @format("+c.format+")")
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
	proj := analyzeFiles(t, map[string]string{
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

// Every shape of a cross-package enum field calls the enum's own Validate.
func TestValidateEmitsCrossPkgEnumAllShapes(t *testing.T) {
	proj := analyzeFiles(t, map[string]string{
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
	proj := analyzeFiles(t, map[string]string{
		"shared/t.craftgo": `package shared
scalar Email string @format(email) @length(1, 64)`,
		"app/t.craftgo": `package app
import "shared"
type Bag { byEmail map<shared.Email, string> }`,
	})
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

// A field typed in another package gets its Validate call only through the project resolver:
// the package alone cannot see that type.
func TestValidateCallsACrossPackageTypeOnlyThroughTheResolver(t *testing.T) {
	proj := analyzeFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
type Owner { name string @minLength(1) }`,
		"app/types.craftgo": `package app
import "shared"
type Product { id string  owner shared.Owner }`,
	})
	for _, c := range []struct {
		name     string
		resolver *projectResolver
		call     bool
	}{
		{"package alone", nil, false},
		{"project resolver", &projectResolver{Resolver: semantic.NewResolver(proj, "app")}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := generateValidators(proj.Packages["app"], dir, c.resolver); err != nil {
				t.Fatal(err)
			}
			src := readGen(t, dir, "app/validate.go")
			mustParseGo(t, src)
			if got := strings.Contains(src, "v.Owner.Validate()"); got != c.call {
				t.Errorf("v.Owner.Validate() emitted = %v, want %v:\n%s", got, c.call, src)
			}
		})
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

// A cross-field message has no subject and names each member as the member's
// own checks do, by its @json key, a mixin-promoted member included.
func TestValidateCrossFieldMessagesNameMembersByWireName(t *testing.T) {
	src := runValidateGen(t, `package design
type Keyed { primary string? @json("primary_email") }
@requiresOneOf(primary, backup)
@mutuallyExclusive(primary, backup)
type Renamed {
    Keyed
    backup string? @json("backup_email")
}`)
	mustContainAll(t, src,
		`fmt.Errorf("requiresOneOf [primary_email backup_email] - at least one must be set")`,
		`fmt.Errorf("mutuallyExclusive [primary_email backup_email] - at most one may be set")`)
	if strings.Contains(src, `"Renamed: `) {
		t.Errorf("a cross-field message names the DSL type:\n%s", src)
	}
}

// A member a GET request auto-binds is named by its query parameter.
func TestValidateCrossFieldMessageNamesAutoBoundParameter(t *testing.T) {
	src := runValidateGen(t, `package design
@requiresOneOf(byName, byId)
type Find {
    byName string? @json("by_name")
    byId   string? @json("by_id")
}
type R { ok bool }
service S { get Find /find { request Find  response R } }`)
	mustContainAll(t, src, `fmt.Errorf("requiresOneOf [byName byId] - at least one must be set")`)
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
	if !strings.Contains(src, `fmt.Errorf("must be one of [Active Inactive Pending]")`) {
		t.Errorf("expected enum error message:\n%s", src)
	}
	if !strings.Contains(src, "if err := v.Status.Validate(); err != nil {") {
		t.Errorf("expected enum field to dispatch through Validate():\n%s", src)
	}
}

// An enum's message lists the values the wire carries: a string member's
// value, an int member's number, a bare member's name.
func TestValidateEnumMessageListsWireValues(t *testing.T) {
	src := runValidateGen(t, `package design
enum Phase { Todo = "todo"  InProgress = "in_progress" }
enum Tier { Bronze = 1  Silver = 2 }
enum Mode { Fast  Safe }
type T { p Phase  t Tier  m Mode }`)
	mustContainAll(t, src,
		`fmt.Errorf("must be one of [todo in_progress]")`,
		`fmt.Errorf("must be one of [1 2]")`,
		`fmt.Errorf("must be one of [Fast Safe]")`)
	for _, dslName := range []string{"Phase", "Tier", "Mode"} {
		if strings.Contains(src, "invalid "+dslName) {
			t.Errorf("an enum message names the DSL type %s:\n%s", dslName, src)
		}
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
	if !strings.Contains(src, `fmt.Errorf("must be one of [Low High]")`) {
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

// A required bare type-parameter field is absent at a nil pointer or
// interface, as a missing `file` or `any` argument is; an optional,
// @nullable or collection one gets no presence check.
func TestValidateTypeParamPresence(t *testing.T) {
	src := runValidateGen(t, `package design
type Box<T> {
    one  T
    opt  T?
    null T @nullable
    many T[]
}`)
	mustContainAll(t, src,
		"if absentValue(&v.One) {",
		`fmt.Errorf("one: required")`,
		"case **multipart.FileHeader:",
	)
	for _, absent := range []string{"absentValue(&v.Opt)", "absentValue(&v.Null)", "absentValue(&v.Many)", `"many: required"`} {
		if strings.Contains(src, absent) {
			t.Errorf("unexpected presence check %s:\n%s", absent, src)
		}
	}
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
	_, diags := semantic.Analyze([]*ast.File{parseDesign(t, "test.craftgo", `package design
type X { name string @maxSize(1024) }`)})
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

// @mimeTypes matches the part's media type, parameters and case aside: an
// exact type, or a `type/*` range by its prefix.
func TestValidateMimeTypes(t *testing.T) {
	src := runValidateGen(t, `package design
type Upload {
    avatar file @mimeTypes(["image/*", "Application/PDF"])
}`)
	mustContainAll(t, src,
		"v.Avatar != nil",
		`mime.ParseMediaType(v.Avatar.Header.Get("Content-Type"))`,
		`strings.HasPrefix(_mt, "image/")`,
		`_mt == "application/pdf"`,
		"disallowed content type",
	)
}

// `*/*` admits every upload, so no check is emitted.
func TestValidateMimeTypesAnyEmitsNothing(t *testing.T) {
	src := runValidateGen(t, `package design
type Upload {
    avatar file @mimeTypes("*/*", "image/png")
}`)
	if strings.Contains(src, "disallowed content type") {
		t.Errorf("*/* still checks the content type:\n%s", src)
	}
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
	if !strings.Contains(src, `_mt == "image/png"`) {
		t.Errorf("missing mimeTypes check:\n%s", src)
	}
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
	proj := analyzeFiles(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Name string @minLength(1)`,
		"app/t.craftgo": `package app
import "shared"
type U { names shared.Name[] @uniqueItems }`,
	})
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
	proj := analyzeFiles(t, map[string]string{
		"m/m.craftgo": `package m
type Body { reqStrArr string[]  reqAnyArr any[] }
type Resp { ok bool }
service S { post Op /x { request Body  response Resp } }`,
	})
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

// A type parameter spelled like a declaration is the parameter: an optional
// one is a pointer its probe reads through, and it takes no check of the
// declaration's kind.
func TestTypeParamShadowsDeclaration(t *testing.T) {
	pkg := analyze(t, `package design
scalar Blob bytes
enum Color { Red  Green }
type Box<Blob> { v Blob? }
type Tagged<Color> { c Color }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	types, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	validate, _ := os.ReadFile(filepath.Join(dir, "design", "validate.go"))
	mustParseGo(t, string(types))
	mustParseGo(t, string(validate))
	if norm := collapseSpace(string(types)); !strings.Contains(norm, "V *Blob `json:\"v,omitempty\"`") {
		t.Errorf("an optional type parameter must be a pointer:\n%s", types)
	}
	if !strings.Contains(string(validate), "any(v.V).(interface{ Validate() error })") || strings.Contains(string(validate), "v.C ==") {
		t.Errorf("a type parameter must be probed through its pointer and get no enum check:\n%s", validate)
	}
}
