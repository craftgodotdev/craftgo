package semantic

import (
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func TestPrimsString(t *testing.T) {
	cases := []struct {
		p    Prims
		want string
	}{
		{0, "any"},
		{PrimString, "string"},
		{PrimNumber, "number"},
		{PrimInteger, "integer"},
		{PrimFloat, "float"},
		{PrimBool, "bool"},
		{PrimArray | PrimMap, "array, map"},
		{PrimFile, "file"},
		{PrimString | PrimBytes, "string, bytes"},
		{PrimString | PrimNumber, "string, number"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("Prims(%d).String() = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestPrimFromName(t *testing.T) {
	cases := []struct {
		in   string
		want Prims
	}{
		{"string", PrimString},
		{"bytes", PrimBytes},
		{"int", PrimInteger},
		{"int64", PrimInteger},
		{"uint8", PrimInteger},
		{"float32", PrimFloat},
		{"bool", PrimBool},
		{"file", PrimFile},
		{"datetime", PrimDateTime},
		{"any", 0}, // not classified at this layer
		{"User", 0},
	}
	for _, c := range cases {
		if got := PrimFromName(c.in); got != c.want {
			t.Errorf("PrimFromName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Each rule a category split carries is decided by AppliesTo alone, once.
func TestAppliesToDecidesSplitCategories(t *testing.T) {
	for _, c := range []struct{ src, msg string }{
		{`type X { b bytes @pattern("^a$") }`, "@pattern applies to string fields, but X.b is bytes"},
		{`type X { b bytes @format(email) }`, "@format(email) applies to string, but X.b is bytes"},
		{"scalar Blob bytes @format(raw)\ntype X { b Blob @format(email) }", "@format(email) applies to string, but X.b is Blob"},
		{`scalar B bytes @format(email)`, "@format(email) applies to string, but scalar B is bytes"},
		{`type X { r float64 @multipleOf(2) }`, "@multipleOf applies to integer fields, but X.r is float"},
		{`type X { m map<string, int> @uniqueItems }`, "@uniqueItems applies to array fields, but X.m is map"},
		{`scalar B bytes @pattern("^a$")`, "@pattern applies to string, but scalar B is bytes"},
		{`scalar R float32 @multipleOf(2)`, "@multipleOf applies to integer, but scalar R is float"},
	} {
		d := expectError(t, c.src, CodeDecoratorTypeMismatch)
		expectMessage(t, d, c.msg)
		expectCodeCount(t, c.src, CodeDecoratorTypeMismatch, 1)
	}
	mustClean(t, `type X { b bytes @minLength(1) @maxLength(9)  m map<string, int> @minItems(1) @maxItems(3)  n int @multipleOf(2) }`)
}

// A field's category follows its resolved type, through scalars and raw bytes.
func TestResolvedFieldPrims(t *testing.T) {
	pkg := mustClean(t, `scalar Email string
scalar Blob bytes @format(raw)
enum S { A }
type X {
	s string
	b bytes
	r bytes @format(raw)
	e Email
	blob Blob
	n int
	f float32
	xs int[]
	m map<string, int>
	en S
	a any
}`)
	want := map[string]Prims{"s": PrimString, "b": PrimBytes, "r": PrimRawBytes, "e": PrimString, "blob": PrimRawBytes,
		"n": PrimInteger, "f": PrimFloat, "xs": PrimArray, "m": PrimMap, "en": 0, "a": 0}
	for _, f := range ast.Fields(pkg.Types["X"].Body) {
		if got := ResolveField(f, pkg, nil).Prims(); got != want[f.Name] {
			t.Errorf("%s: Prims = %v, want %v", f.Name, got, want[f.Name])
		}
	}
}

func TestStringValidatorsOnString(t *testing.T) {
	mustClean(t, `type X { name string @length(1, 20) @pattern("^[a-z]+$") }`)
}

func TestStringValidatorOnIntRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { age int @length(1, 5) }`))
	d := findCode(diags, CodeDecoratorTypeMismatch)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "string") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestPatternOnBoolRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { active bool @pattern("yes|no") }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestNumberValidatorsOnInt(t *testing.T) {
	mustClean(t, `type X { age int @gte(0) @lte(120) @multipleOf(1) }`)
}

func TestNumberValidatorOnStringRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @gte(1) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestPositiveOnFloat(t *testing.T) {
	mustClean(t, `type X { ratio float64 @positive }`)
}

func TestArrayValidatorsOnArray(t *testing.T) {
	mustClean(t, `type X { tags string[] @minItems(1) @maxItems(10) @uniqueItems }`)
}

func TestArrayValidatorOnStringRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @minItems(1) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestArrayValidatorOnMap(t *testing.T) {
	// The item-count validators take a map too.
	mustClean(t, `type X { meta map<string, string> @maxItems(50) }`)
}

func TestFileValidatorsOnFile(t *testing.T) {
	mustClean(t, `type X { avatar file @maxSize(5MB) @mimeTypes(["image/png"]) }`)
}

func TestFileValidatorOnStringRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { url string @mimeTypes("image/png") }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestStringValidatorOnStringScalar(t *testing.T) {
	mustClean(t, `scalar Email string @format(email)
type X { addr Email @minLength(5) }`)
}

func TestNumberValidatorOnNumberScalar(t *testing.T) {
	mustClean(t, `scalar Age int @gte(0) @lte(150)
type X { who Age @positive }`)
}

func TestStringValidatorOnNumberScalarRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `scalar Age int
type X { who Age @length(1, 5) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestScalarTypeMismatch(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `scalar Bad int @length(1, 5)`))
	d := findCode(diags, CodeDecoratorTypeMismatch)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "scalar Bad") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestScalarUnknownPrimitiveRejected(t *testing.T) {
	d := expectDiag(t, `scalar Weird unknownPrim`, CodeScalarBadPrimitive)
	expectMessage(t, d, "Weird", "unknownPrim")
	// The message lists every primitive a scalar may wrap.
	expectMessage(t, d, "expected one of "+strings.Join(ScalarPrimitives(), ", "))
}

// A scalar cannot wrap `datetime`, `file` or `any`; the message names the
// built-in and lists the primitives a scalar wraps.
func TestScalarOverUnwrappableBuiltinRejected(t *testing.T) {
	for _, prim := range []string{"datetime", "file", "any"} {
		d := expectError(t, "scalar When "+prim, CodeScalarBadPrimitive)
		expectMessage(t, d, `scalar "When" cannot wrap `+prim, "expected one of string, bool, int,", "bytes")
		if slices.Contains(ScalarPrimitives(), prim) {
			t.Errorf("ScalarPrimitives lists %s", prim)
		}
	}
}

// The diagnostics state the rules analysis applies: a `@group` replaces the
// service's directory, and `@form` binds a single-level `file[]`.
func TestDiagnosticsStateTheRules(t *testing.T) {
	d := expectError(t, "@group(\"..\")\nservice S { get A /a {} }", CodeDecoratorArgValue)
	expectMessage(t, d, "in place of the service's own")
	d = expectError(t, "type R { m map<string, int> @form }", CodeBindingType)
	expectMessage(t, d, "a single-level array of those, `file[]` included")
	mustClean(t, "type R { files file[] @form }")
}

func TestScalarSelfReferenceRejected(t *testing.T) {
	d := expectDiag(t, `scalar Check Check`, CodeScalarBadPrimitive)
	expectMessage(t, d, "Check")
}

// The type-compat check skips a field whose qualified type does not resolve.
func TestQualifiedFieldTypeSkipsCompat(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { user shared.User @length(1, 5) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) != nil {
		t.Errorf("type-compat should not stack on unknown qualified ref, got %v", codes(diags))
	}
}

// The type-compat checks skip unknown decorators on fields and scalars.
func TestTypeCompatSkipsUnknownDecorators(t *testing.T) {
	a := newTestAnalyzer(&Package{
		Scalars: map[string]*ast.ScalarDecl{},
	})
	field := &ast.Field{
		Name: "name",
		Type: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
		Decorators: []*ast.Decorator{
			{Name: "unknownDecorator"},
			// @doc applies to any primitive.
			{Name: "doc", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
		},
	}
	a.checkBodyTypeCompat("X", []ast.TypeMember{field})

	a.checkScalarTypeCompat(&ast.ScalarDecl{
		Name: "S", Primitive: "string",
		Decorators: []*ast.Decorator{{Name: "unknownDecorator"}},
	})
	if len(a.diags) != 0 {
		t.Errorf("unknown decorators should not diag, got %v", a.diags)
	}
}

// A scalar over `file` is rejected.
func TestScalarOverFileRejected(t *testing.T) {
	expectError(t, `scalar FileScalar file
type R { f FileScalar }`, CodeScalarBadPrimitive)
}

// Required, optional and @nullable `bytes @format(raw)` fields are accepted.
func TestRawFormatOnBytes(t *testing.T) {
	mustClean(t, `type X { payload bytes @format(raw)  meta bytes? @format(raw)  trace bytes @format(raw) @nullable }`)
}

// @format(raw) on anything but bytes is rejected, naming the field's type.
func TestRawFormatOffBytesRejected(t *testing.T) {
	cases := []struct {
		name, src, spelt string
	}{
		{"string", `type X { s string @format(raw) }`, "string"},
		{"int32", `type X { s int32 @format(raw) }`, "int32"},
		{"int64", `type X { s int64 @format(raw) }`, "int64"},
		{"int", `type X { s int @format(raw) }`, "int"},
		{"float64", `type X { s float64 @format(raw) }`, "float64"},
		{"bool", `type X { s bool @format(raw) }`, "bool"},
		{"any", `type X { s any @format(raw) }`, "any"},
		{"datetime", `type X { s datetime @format(raw) }`, "datetime"},
		{"enum", "enum E { A B }\ntype X { s E @format(raw) }", "E"},
		{"declared type", "type Y { a string }\ntype X { s Y @format(raw) }", "Y"},
		{"map", `type X { s map<string, bytes> @format(raw) }`, "map<string, bytes>"},
		{"scalar over string", `scalar S string @format(raw)`, "string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := expectError(t, c.src, CodeDecoratorTypeMismatch)
			expectMessage(t, d, "@format(raw) applies to bytes", "is "+c.spelt)
		})
	}
}

// @format(raw) on a bytes array is rejected, as `string[] @format(email)` is.
func TestRawFormatOnBytesArrayRejected(t *testing.T) {
	d := expectError(t, `type X { blobs bytes[] @format(raw) }`, CodeDecoratorTypeMismatch)
	expectMessage(t, d, "@format(raw) applies to bytes", "is bytes[]")
	expectError(t, `type X { emails string[] @format(email) }`, CodeDecoratorTypeMismatch)
}

// A raw bytes scalar is accepted, and a field of it may repeat @format(raw).
func TestRawFormatScalar(t *testing.T) {
	mustClean(t, `scalar RawDoc bytes @format(raw)
type X { photos RawDoc?  notes RawDoc @format(raw) }`)
}

// A raw bytes field or scalar takes no other validator.
func TestRawBytesTakesNoOtherValidator(t *testing.T) {
	for _, src := range []string{
		`type X { payload bytes @format(raw) @minLength(1) }`,
		`type X { payload bytes @format(raw) @gte(1) }`,
		`type X { payload bytes @format(raw) @pattern("^a$") }`,
		`scalar B bytes @format(raw) @minLength(1)`,
	} {
		d := expectError(t, src, CodeDecoratorTypeMismatch)
		expectMessage(t, d, "bytes @format(raw)")
	}
}

// A cross-field group accepts a raw bytes member (nil only when absent) but not a plain `bytes?`.
func TestCrossFieldAcceptsARawBytesMember(t *testing.T) {
	mustClean(t, `@requiresOneOf(left, right)
type Choice { left bytes? @format(raw)  right string? }`)
	expectError(t, `@requiresOneOf(left, right)
type Choice { left bytes?  right string? }`, CodeCrossFieldNotOptional)
}

// A constraint decorator on a struct-typed field is a type mismatch; an enum
// field keeps its backing type's constraints.
func TestConstraintOnStructFieldRejected(t *testing.T) {
	const src = `package p
type Addr { city string }
type Page<T> { items T[] }
enum Tier { Gold = "gold" }
type Req {
	a Addr @gt(3)
	b Addr @minLength(2)
	c Addr @uniqueItems
	d Page<Addr> @maxItems(3)
	e Tier @minLength(2)
}
`
	expectCodeCount(t, src, CodeDecoratorTypeMismatch, 4)
	expectMessage(t, expectDiag(t, src, CodeDecoratorTypeMismatch), "is struct")
}
