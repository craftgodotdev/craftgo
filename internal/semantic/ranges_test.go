package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func TestLengthMinExceedsMax(t *testing.T) {
	d := expectDiag(t, `type X { name string @length(20, 5) }`, CodeDecoratorRange)
	expectMessage(t, d, "min", "max")
}

func TestLengthNegativeMin(t *testing.T) {
	expectDiag(t, `type X { name string @length(-1, 5) }`, CodeDecoratorRange)
}

func TestLengthSingleArgOK(t *testing.T) {
	// @length(5) is an exact length.
	mustClean(t, `type X { name string @length(5) }`)
}

func TestRangeMinExceedsMax(t *testing.T) {
	expectDiag(t, `type X { score int @range(100, 1) }`, CodeDecoratorRange)
}

func TestRangeOK(t *testing.T) {
	mustClean(t, `type X { score int @range(0, 100) }`)
}

func TestMultipleOfZeroRejected(t *testing.T) {
	expectDiag(t, `type X { n int @multipleOf(0) }`, CodeDecoratorRange)
}

func TestMultipleOfNonZeroOK(t *testing.T) {
	mustClean(t, `type X { n int @multipleOf(2) }`)
	// A whole-valued float divisor folds to an int.
	mustClean(t, `type X { n int @multipleOf(5.0) }`)
}

func TestMultipleOfFractionalOnIntRejected(t *testing.T) {
	expectDiag(t, `type X { n int @multipleOf(2.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, "scalar Step int @multipleOf(2.5)", CodeDecoratorTypeMismatch)
}

func TestNegativeOnUnsignedRejected(t *testing.T) {
	expectDiag(t, `type X { count uint @negative }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { n uint32 @negative }`, CodeDecoratorTypeMismatch)
	// On a field typed as an unsigned scalar...
	expectDiag(t, "scalar Qty uint\ntype X { q Qty @negative }", CodeDecoratorTypeMismatch)
	// ...and on the scalar declaration itself.
	expectDiag(t, `scalar Qty uint @negative`, CodeDecoratorTypeMismatch)
}

func TestNegativeOnSignedAndPositiveOnUnsignedOK(t *testing.T) {
	mustClean(t, `type X { delta int @negative }`)
	mustClean(t, `type X { count uint @positive }`)
}

func TestStatusOutOfRange(t *testing.T) {
	expectDiag(t, `service S {
	@status(99)
	get GetUser /u {}
}`, CodeDecoratorRange)
}

func TestStatusTooHigh(t *testing.T) {
	expectDiag(t, `service S {
	@status(600)
	get GetUser /u {}
}`, CodeDecoratorRange)
}

func TestStatusValidOK(t *testing.T) {
	mustClean(t, `service S {
	@status(201)
	post Create /c {}
}`)
}

func TestZeroDurationRejected(t *testing.T) {
	expectDiag(t, `service S {
	@timeout(0)
	get G /g {}
}`, CodeDecoratorRange)
}

func TestNegativeDurationRejected(t *testing.T) {
	expectDiag(t, `service S {
	@timeout(-1)
	get G /g {}
}`, CodeDecoratorRange)
}

func TestPositiveDurationLiteralAccepted(t *testing.T) {
	mustClean(t, `service S {
	@timeout(5s)
	get G /g {}
}`)
}

// A zero duration literal `0s` is rejected like a bare 0.
func TestZeroDurationLiteralRejected(t *testing.T) {
	expectDiag(t, `service S {
	@timeout(0s)
	get G /g {}
}`, CodeDecoratorRange)
}

// Bare seconds past a Go duration's range are rejected.
func TestOverflowingDurationRejected(t *testing.T) {
	d := expectError(t, `service S {
	@timeout(9999999999)
	get G /g {}
}`, CodeDecoratorRange)
	expectMessage(t, d, "9999999999", "out of range")
}

func TestZeroSizeRejected(t *testing.T) {
	expectDiag(t, `service S {
	@maxBodySize(0)
	get G /g {}
}`, CodeDecoratorRange)
}

func TestPositiveSizeLiteralAccepted(t *testing.T) {
	mustClean(t, `service S {
	@maxBodySize(1MB)
	get G /g {}
}`)
}

// A zero size literal `0B` is rejected like a bare 0.
func TestZeroSizeLiteralRejected(t *testing.T) {
	expectDiag(t, `service S {
	@maxBodySize(0B)
	get G /g {}
}`, CodeDecoratorRange)
}

// A size literal past int64 is rejected.
func TestOverflowingSizeLiteralRejected(t *testing.T) {
	d := expectDiag(t, `service S {
	@maxBodySize(99999999999999999999GB)
	get G /g {}
}`, CodeDecoratorRange)
	expectMessage(t, d, "is not a byte size")
}

// A zero field-level @maxSize is rejected.
func TestZeroMaxSizeRejected(t *testing.T) {
	expectDiag(t, `type Req { avatar file @maxSize(0MB) }`, CodeDecoratorRange)
}

func TestMinLengthNegative(t *testing.T) {
	expectDiag(t, `type X { name string @minLength(-1) }`, CodeDecoratorRange)
}

func TestMinLengthExceedsMaxLength(t *testing.T) {
	d := expectDiag(t, `type X { name string @minLength(10) @maxLength(5) }`, CodeDecoratorRange)
	if len(d.Related) != 1 {
		t.Errorf("expected related to @minLength, got %+v", d.Related)
	}
}

func TestMinItemsExceedsMaxItems(t *testing.T) {
	expectDiag(t, `type X { tags string[] @minItems(10) @maxItems(2) }`, CodeDecoratorRange)
}

func TestEmptyRangeStrictPair(t *testing.T) {
	// Equal endpoints with a strict side admit no value.
	expectError(t, `type X { v int @gt(5) @lt(5) }`, CodeBoundEmptyRange)
	expectError(t, `type X { v int @gte(5) @lt(5) }`, CodeBoundEmptyRange)
	expectError(t, `type X { v int @gt(5) @lte(5) }`, CodeBoundEmptyRange)
	// `@gte(N) @lte(N)` admits exactly N.
	mustClean(t, `type X { v int @gte(5) @lte(5) }`)
}

// A sign constraint that another bound contradicts leaves no value, on a
// field and on a scalar, and is rejected at the upper bound.
func TestContradictingSignConstraintsRejected(t *testing.T) {
	for _, c := range []struct{ decs, code, msg string }{
		{"@positive @negative", CodeBoundEmptyRange, "@negative contradicts @positive: no value is both > 0 and < 0"},
		{"@positive @lte(0)", CodeBoundEmptyRange, "@lte(0) contradicts @positive"},
		{"@positive @lt(0)", CodeBoundEmptyRange, "@lt(0) contradicts @positive"},
		{"@negative @gte(0)", CodeBoundEmptyRange, "@negative contradicts @gte(0)"},
		{"@negative @gt(0.0)", CodeBoundEmptyRange, "@negative contradicts @gt(0.0)"},
		{"@positive @lte(-3)", CodeDecoratorRange, "@lte(-3) contradicts @positive: no value is both > 0 and ≤ -3"},
		{"@negative @gte(2)", CodeDecoratorRange, "@negative contradicts @gte(2)"},
		{"@negative @range(1, 5)", CodeDecoratorRange, "@negative contradicts @range(1, 5): no value is both ≥ 1 and < 0"},
		{"@positive @range(-5, -1)", CodeDecoratorRange, "@range(-5, -1) contradicts @positive"},
	} {
		for _, src := range []string{"type X { v int " + c.decs + " }", "scalar S int " + c.decs} {
			d := expectError(t, src, c.code)
			expectMessage(t, d, c.msg)
		}
	}
	mustClean(t, `type X { v int @positive @lte(10)  w float64 @negative @gte(-0.5) }`)
}

// An exact or ranged @length bounds the length on both sides, so a
// @minLength or @maxLength outside it leaves no length.
func TestLengthContradictingMinMaxLength(t *testing.T) {
	d := expectError(t, `type X { s string @length(5) @maxLength(3) }`, CodeDecoratorRange)
	expectMessage(t, d, "@maxLength(3) contradicts @length(5): no length is both ≥ 5 and ≤ 3")
	expectError(t, `type X { s string @length(1, 4) @minLength(6) }`, CodeDecoratorRange)
}

// Bounds compare by their exact values: past 2^53 two whole floats that
// share a float64 still differ.
func TestBoundsCompareExactly(t *testing.T) {
	mustClean(t, `type X { v int64 @gte(9007199254740992.0) @lt(9007199254740993.0) }`)
	expectError(t, `type X { v int64 @gte(9007199254740993.0) @lte(9007199254740992.0) }`, CodeDecoratorRange)
}

func TestMultipleOfNegativeRejected(t *testing.T) {
	expectDiag(t, `type X { n int @multipleOf(-2) }`, CodeDecoratorRange)
}

func TestCrossFieldDuplicateRef(t *testing.T) {
	expectDiag(t, `@requiresOneOf(a, a, b)
type X { a string? b string? }`, CodeDuplicateGroupField)
}

func TestMutuallyExclusiveSingleField(t *testing.T) {
	expectDiag(t, `@mutuallyExclusive(only)
type X { only string? }`, CodeMutExSingleField)
}

func TestBoundOverflowInt8(t *testing.T) {
	expectDiag(t, `type X { score int8 @lte(300) }`, CodeBoundOverflow)
	expectDiag(t, `type X { neg int8 @gte(-200) }`, CodeBoundOverflow)
	expectDiag(t, `type X { u uint @lt(-1) }`, CodeBoundOverflow)
	// Values in range are accepted.
	mustClean(t, `type X { score int8 @lte(127) @gte(-128) }`)
	mustClean(t, `type X { u uint8 @range(0, 255) }`)
}

func TestMinExceedsMax(t *testing.T) {
	expectDiag(t, `type X { score int @gte(100) @lte(10) }`, CodeDecoratorRange)
}

func TestFractionalBoundOnIntRejected(t *testing.T) {
	d := expectDiag(t, `type X { count int @gte(0.5) }`, CodeDecoratorTypeMismatch)
	expectMessage(t, d, "whole number", "count")
	expectDiag(t, `type X { count int @lte(10.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @gt(0.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @lt(9.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @range(0.5, 10) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @range(0, 10.5) }`, CodeDecoratorTypeMismatch)
	// A field typed as a local integer scalar is rejected too.
	expectDiag(t, `scalar Count int
type X { n Count @gte(0.5) }`, CodeDecoratorTypeMismatch)
}

func TestFractionalBoundOnScalarRejected(t *testing.T) {
	expectDiag(t, `scalar Half int @gte(0.5)`, CodeDecoratorTypeMismatch)
	expectDiag(t, `scalar Half uint8 @range(0.5, 9)`, CodeDecoratorTypeMismatch)
}

func TestFractionalBoundOnFloatOK(t *testing.T) {
	mustClean(t, `type X { ratio float64 @gte(0.5) @lte(1.5) }`)
	mustClean(t, `type X { ratio float32 @range(0.1, 0.9) }`)
	mustClean(t, `scalar Half float64 @gte(0.5)`)
}

func TestFloat32BoundOverflow(t *testing.T) {
	const huge = "400000000000000000000000000000000000000.0" // 4e38 > MaxFloat32
	expectDiag(t, `type X { r float32 @gte(`+huge+`) }`, CodeBoundOverflow)
	expectDiag(t, `type X { r float32 @lte(`+huge+`) }`, CodeBoundOverflow)
	expectDiag(t, `type X { r float32 @range(0, `+huge+`) }`, CodeBoundOverflow)
	expectDiag(t, `scalar Big float32 @lte(`+huge+`)`, CodeBoundOverflow)
	// Within float32 range, and any magnitude on float64, are clean.
	mustClean(t, `type X { r float32 @gte(-100.5) @lte(100.5) }`)
	mustClean(t, `type X { r float64 @lte(`+huge+`) }`)
}

func TestIntegralFloatBoundOnIntOK(t *testing.T) {
	mustClean(t, `type X { count int @gte(1.0) @lte(10.0) }`)
	mustClean(t, `type X { count int @range(0.0, 100.0) }`)
}

func TestMinMaxOnlyOneSide(t *testing.T) {
	mustClean(t, `type X { score int @gte(0) }`)
	mustClean(t, `type X { name string @maxLength(50) }`)
}

// @nullable on a `T?` field warns as redundant.
func TestNullableOnOptionalIsWarning(t *testing.T) {
	d := expectWarning(t, `type X { name string? @nullable }`, CodeDecoratorRedundant)
	expectMessage(t, d, "redundant")
}

func TestNullableOnNonOptionalOK(t *testing.T) {
	mustClean(t, `type X { name string @nullable }`)
}

func TestScalarRangeChecked(t *testing.T) {
	expectDiag(t, `scalar Score int @range(100, 1)`, CodeDecoratorRange)
}

// A scalar's constraints obey every value rule a field's do, each reported once.
func TestScalarAndFieldShareValueRules(t *testing.T) {
	for _, c := range []struct{ prim, decs, code string }{
		{"int8", "@lte(300)", CodeBoundOverflow},
		{"float32", "@lte(400000000000000000000000000000000000000.0)", CodeBoundOverflow},
		{"int", "@gte(0.5)", CodeDecoratorTypeMismatch},
		{"int", "@multipleOf(2.5)", CodeDecoratorTypeMismatch},
		{"uint", "@negative", CodeDecoratorTypeMismatch},
		{"uint", "@lt(0)", CodeDecoratorTypeMismatch},
		{"int", "@gte(10) @lte(1)", CodeDecoratorRange},
		{"int", "@gt(5) @lt(5)", CodeBoundEmptyRange},
	} {
		expectCodeCount(t, "scalar S "+c.prim+" "+c.decs, c.code, 1)
		expectCodeCount(t, "type X { v "+c.prim+" "+c.decs+" }", c.code, 1)
	}
	// An array takes no value rule; its type mismatch is the one report.
	expectCodeCount(t, "type X { xs uint[] @negative }", CodeDecoratorTypeMismatch, 1)
}

// A bound decorator whose arguments are missing or not numbers puts no bound.
func TestDeclaredBoundsSkipUnreadable(t *testing.T) {
	bs := declaredBounds([]*ast.Decorator{
		{Name: "gte"},
		{Name: "lte", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{}}}},
		{Name: "range", Args: []*ast.DecoratorArg{{Value: &ast.IntLit{Value: 1}}, {Value: &ast.StringLit{}}}},
		{Name: "positive"},
	})
	if len(bs) != 1 || bs[0].dec.Name != "positive" {
		t.Errorf("want only @positive's bound, got %+v", bs)
	}
}

func TestBodyRangesSkipMixins(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.checkBodyRanges([]ast.TypeMember{
		// Mixin members are skipped.
		&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Other"}}}},
	}, nil)
	if len(a.diags) != 0 {
		t.Errorf("expected no diags, got %v", a.diags)
	}
}

// The range helpers report nothing for a wrong arity or a non-numeric argument.
// A decorator whose arguments break its shape gets that error alone; its
// value rules run only on a well-formed decorator.
func TestValueRulesSkipAMalformedDecorator(t *testing.T) {
	for _, c := range []struct{ src, code string }{
		{`type X { s string @length() }`, CodeDecoratorArity},
		{`type X { s string @length(-1, 2, 3) }`, CodeDecoratorArity},
		{`type X { n int @multipleOf("0") }`, CodeDecoratorArgType},
		{`type X { s string @pattern("(", "x") }`, CodeDecoratorArity},
		{`type X { s string @minLength(-1, 2) }`, CodeDecoratorArity},
		{"type R { ok bool }\nservice S {\n  @status(99, 1)\n  get A /a { response R }\n}", CodeDecoratorArity},
		{"type R { ok bool }\nservice S {\n  @timeout(0, 1)\n  get A /a { response R }\n}", CodeDecoratorArity},
		{"@group(\"..\", \"x\")\nservice S { get A /a {} }", CodeDecoratorArity},
	} {
		_, diags := Analyze(parseFiles(t, c.src))
		if len(diags) != 1 || diags[0].Code != c.code {
			t.Errorf("%s: want only %s, got %v", c.src, c.code, diags)
		}
	}
}

func TestUniqueItemsNonComparableRejected(t *testing.T) {
	// The element must be usable as a Go map key.
	expectDiag(t, `type T { twoD string[][] @uniqueItems }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type T { a any[] @uniqueItems }`, CodeDecoratorTypeMismatch)
	expectDiag(t, "type NC { rows string[] }\ntype T { s NC[] @uniqueItems }", CodeDecoratorTypeMismatch)
	expectDiag(t, "type Page<X> { items X[]  total int }\ntype T { p Page<string>[] @uniqueItems }", CodeDecoratorTypeMismatch)
	// Pair<bytes> holds []byte fields, so it is not comparable.
	expectDiag(t, "type Pair<X> { a X  b X }\ntype T { ps Pair<bytes>[] @uniqueItems }", CodeDecoratorTypeMismatch)
}

func TestUniqueItemsComparableOK(t *testing.T) {
	mustClean(t, `type T { tags string[] @uniqueItems  nums int[] @uniqueItems }`)
	mustClean(t, "scalar Tag string @minLength(1)\ntype T { tags Tag[] @uniqueItems }")
	mustClean(t, "enum Color { Red  Blue }\ntype T { cs Color[] @uniqueItems }")
	mustClean(t, "type Pt { x int  y int }\ntype T { pts Pt[] @uniqueItems }")
	// A generic instance over a comparable argument is comparable.
	mustClean(t, "type Pair<X> { a X  b X }\ntype T { ps Pair<int>[] @uniqueItems }")
}

func TestMapKeyNotMarshalableRejected(t *testing.T) {
	// A generic type-parameter key lowers to `map[K any]` - invalid Go.
	expectDiag(t, "type Item { id int }\ntype Index<K> { byKey map<K, Item> }", CodeMapKeyType)
	// A struct with a slice field is not comparable.
	expectDiag(t, "type Item { tags string[] }\ntype Bad { m map<Item, string> }", CodeMapKeyType)
	// A comparable struct key compiles, but json.Marshal cannot encode it.
	expectDiag(t, "type Key { id int  region string }\ntype Bag { m map<Key, string> }", CodeMapKeyType)
	// json.Marshal rejects bool and float keys at run time.
	expectDiag(t, "type V { x int }\ntype Bag { m map<bool, V> }", CodeMapKeyType)
	expectDiag(t, "type V { x int }\ntype Bag { m map<float64, V> }", CodeMapKeyType)
	// A scalar over bool or float is rejected too.
	expectDiag(t, "scalar Flag bool\ntype V { x int }\ntype Bag { m map<Flag, V> }", CodeMapKeyType)
	expectDiag(t, "scalar Ratio float64\ntype V { x int }\ntype Bag { m map<Ratio, V> }", CodeMapKeyType)
}

func TestMapKeyMarshalableOK(t *testing.T) {
	mustClean(t, "type Item { id int }\ntype Bag { m map<string, Item> }")
	// String- and int-backed scalars and enums are valid keys.
	mustClean(t, "scalar UserID int @gte(1)\ntype Item { id int }\ntype Bag { m map<UserID, Item> }")
	mustClean(t, "enum Color { Red  Blue }\ntype Item { id int }\ntype Bag { m map<Color, Item> }")
	// Nested maps with string / int keys.
	mustClean(t, "type V { x int }\ntype Bag { m map<string, map<int, V>> }")
}

// `@lt(0)` on an unsigned field is rejected like @negative.
func TestUnsignedLtZeroRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type T { c uint16 @lt(0) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected @lt(0)-on-unsigned rejection; got %v", codes(diags))
	}
}

// `@lt(N)` with N > 0 on an unsigned field is accepted.
func TestUnsignedLtPositiveClean(t *testing.T) {
	mustClean(t, `type T { c uint16 @lt(10) }`)
}

// `@lt(0.0)` on an unsigned field is rejected like `@lt(0)`.
func TestUnsignedLtZeroFloatRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type T { c uint16 @lt(0.0) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected @lt(0.0)-on-unsigned rejection; got %v", codes(diags))
	}
}

// A positive float `@lt` on an unsigned field is accepted.
func TestUnsignedLtPositiveFloatClean(t *testing.T) {
	mustClean(t, `type T { c uint16 @lt(10.0) }`)
}

// An integral float bound past uint64 is rejected, and the message shows it as a whole number.
func TestFloatBoundOverflowRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type T { c uint64 @lte(20000000000000000000.0) }`))
	d := findCode(diags, CodeBoundOverflow)
	if d == nil {
		t.Fatalf("expected capacity-overflow rejection for out-of-range float bound; got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "20000000000000000000") {
		t.Errorf("overflow message should show the whole-number bound, got: %s", d.Msg)
	}
}

// An integral float bound within uint64 is accepted.
func TestFloatBoundInRangeClean(t *testing.T) {
	mustClean(t, `type T { c uint64 @lte(18000000000000000000.0) }`)
}

// `@lt(0.0)` on a cross-package unsigned scalar is rejected.
func TestCrossPkgUnsignedLtZeroFloatRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Count uint32`,
		"api.craftgo": `package design
import "shared"
type T1 { n shared.Count @lt(0.0) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected cross-pkg @lt(0.0)-on-unsigned rejection; got %v", codes(diags))
	}
}

// Contradictory and overflowing bounds on a cross-package unsigned scalar are rejected.
func TestCrossPkgUnsignedBoundRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Count uint32`,
		"api.craftgo": `package design
import "shared"
type T1 { n shared.Count @lt(0) }
type T2 { m shared.Count @lte(-1) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeDecoratorTypeMismatch) || !hasCode(diags, CodeBoundOverflow) {
		t.Fatalf("expected unsigned @lt(0) + capacity-overflow rejections; got %v", codes(diags))
	}
}

// A scalar declaration rejects a bound that overflows its primitive.
func TestScalarDeclBoundCapacityRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar X uint8 @lte(300)\n",
		"package p\nscalar X int8 @gte(200)\n",
		"package p\nscalar X uint16 @gt(70000)\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "exceeds") {
			t.Errorf("expected capacity reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// An unsigned scalar declaration rejects @lt(0) and @negative.
func TestScalarDeclUnsignedContradictionRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar X uint @lt(0)\n",
		"package p\nscalar X uint8 @negative\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "cannot apply to an unsigned") {
			t.Errorf("expected unsigned-contradiction reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// An in-range bound on a scalar declaration is accepted.
func TestScalarDeclBoundInRangeClean(t *testing.T) {
	diags := analyzeOneFile(t, "package p\nscalar X uint8 @lte(200) @gte(1)\n")
	if hasDiagContaining(diags, "exceeds") {
		t.Errorf("in-range scalar bound wrongly rejected: %v", diags)
	}
}

// A negative exact length `@length(-1)` is rejected.
func TestNegativeExactLengthRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype T { a string @length(-1) }\n")
	if !hasDiagContaining(diags, "exact length must be") {
		t.Errorf("expected @length(-1) reject, got: %v", diags)
	}
}

// An integral float bound that overflows the field's primitive is rejected.
func TestIntegralFloatBoundCapacityRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype T { a int8 @gte(300.0) }\n")
	if !hasDiagContaining(diags, "exceeds") {
		t.Errorf("expected integral-float capacity reject, got: %v", diags)
	}
}

// A whole float at an integer primitive's limit is held to the exact range: one past it is
// rejected, the limit itself accepted.
func TestIntegerLimitFloatBoundCapacity(t *testing.T) {
	for _, src := range []string{
		"package p\ntype T { a int64 @lte(9223372036854775808.0) }\n",
		"package p\ntype T { a int64 @gte(-9223372036854775809.0) }\n",
		"package p\ntype T { a uint64 @multipleOf(18446744073709551616.0) }\n",
	} {
		if diags := analyzeOneFile(t, src); !hasDiagContaining(diags, "exceeds") {
			t.Errorf("expected capacity reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
	for _, src := range []string{
		"package p\ntype T { a int64 @lte(9223372036854775807.0) @gte(-9223372036854775808.0) }\n",
		"package p\ntype T { a uint64 @multipleOf(18446744073709551615.0) }\n",
	} {
		if diags := analyzeOneFile(t, src); hasDiagContaining(diags, "exceeds") {
			t.Errorf("in-range bound wrongly rejected for %q: %v", strings.TrimSpace(src), diags)
		}
	}
}

// A scalar declaration with contradictory pair bounds is rejected.
func TestScalarDeclPairOrderingRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar Score int @gte(100) @lte(10)\n",
		"package p\nscalar Name string @minLength(10) @maxLength(5)\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "contradicts") {
			t.Errorf("expected scalar pair-ordering reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}
