package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ---------- Prims rendering ----------

func TestPrimsString(t *testing.T) {
	cases := []struct {
		p    Prims
		want string
	}{
		{0, "any"},
		{PrimString, "string"},
		{PrimNumber, "number"},
		{PrimBool, "bool"},
		{PrimArray, "array"},
		{PrimFile, "file"},
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
		{"bytes", PrimString},
		{"int", PrimNumber},
		{"int64", PrimNumber},
		{"uint8", PrimNumber},
		{"float32", PrimNumber},
		{"bool", PrimBool},
		{"file", PrimFile},
		{"any", 0}, // not classified at this layer
		{"User", 0},
	}
	for _, c := range cases {
		if got := primFromName(c.in); got != c.want {
			t.Errorf("primFromName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---------- String validators ----------

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

// ---------- Number validators ----------

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

// ---------- Array validators ----------

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
	// Maps share the array category.
	mustClean(t, `type X { meta map<string, string> @maxItems(50) }`)
}

// ---------- File validators ----------

func TestFileValidatorsOnFile(t *testing.T) {
	mustClean(t, `type X { avatar file @maxSize(5MB) @mimeTypes(["image/png"]) }`)
}

func TestFileValidatorOnStringRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { url string @mimeTypes("image/png") }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- Scalar resolution ----------

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

// ---------- Scalar declarations themselves ----------

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
	// A scalar's primitive slot must hold a built-in identifier.
	// Typos like `scalar Weird unknownPrim` or self-references like
	// `scalar Check Check` used to be silently accepted - the
	// generated Go would compile but the inherited validators
	// vanished. The check now flags the bad primitive at design
	// time so the surprise lands before `craftgo gen`.
	d := expectDiag(t, `scalar Weird unknownPrim`, CodeScalarBadPrimitive)
	expectMessage(t, d, "Weird", "unknownPrim")
}

func TestScalarSelfReferenceRejected(t *testing.T) {
	// Regression: `scalar Name Name` declares a scalar that aliases
	// itself - syntactically a noun in the primitive slot, but
	// semantically meaningless (infinite recursion if the codegen
	// ever tried to resolve the underlying primitive).
	d := expectDiag(t, `scalar Check Check`, CodeScalarBadPrimitive)
	expectMessage(t, d, "Check")
}

// ---------- Unresolved type silently skipped ----------

func TestQualifiedFieldTypeSkipsCompat(t *testing.T) {
	// The qualified-ref pass already flags shared.User; the type-compat
	// check should silently skip (nil primitive) so the user only sees
	// one diagnostic per source location.
	_, diags := Analyze(parseFiles(t, `type X { user shared.User @length(1, 5) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) != nil {
		t.Errorf("type-compat should not stack on unknown qualified ref, got %v", codes(diags))
	}
}

// ---------- nil-shape defensive ----------

func TestFieldPrimNil(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	if got := a.fieldPrim(nil); got != 0 {
		t.Errorf("nil TypeRef should resolve to 0, got %v", got)
	}
}

// TestTypeCompatNilDecoratorTolerated covers the defensive nil-entry
// guards in checkBodyTypeCompat / checkScalarTypeCompat. Parser doesn't
// emit nil entries today, so we hand-build the scopes.
func TestTypeCompatNilDecoratorTolerated(t *testing.T) {
	a := &analyzer{pkg: &Package{
		Scalars: map[string]*ast.ScalarDecl{},
	}}
	field := &ast.Field{
		Name: "name",
		Type: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
		Decorators: []*ast.Decorator{
			nil,
			// Unknown decorator - placement pass would flag, type-compat skips.
			{Name: "unknownDecorator"},
			// Known decorator with AppliesTo == 0 (PrimAny) - no-op.
			{Name: "doc", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{Value: "x"}}}},
		},
	}
	a.checkBodyTypeCompat("X", []ast.TypeMember{field})

	// Same for the scalar walker.
	a.checkScalarTypeCompat(&ast.ScalarDecl{
		Name: "S", Primitive: "string",
		Decorators: []*ast.Decorator{
			nil,
			{Name: "unknownDecorator"},
		},
	})
	if len(a.diags) != 0 {
		t.Errorf("nil/unknown decorators should not diag, got %v", a.diags)
	}
}
