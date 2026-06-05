package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ---------- @length pair ----------

func TestLengthMinExceedsMax(t *testing.T) {
	d := expectDiag(t, `type X { name string @length(20, 5) }`, CodeDecoratorRange)
	expectMessage(t, d, "min", "max")
}

func TestLengthNegativeMin(t *testing.T) {
	expectDiag(t, `type X { name string @length(-1, 5) }`, CodeDecoratorRange)
}

func TestLengthSingleArgOK(t *testing.T) {
	// @length(5) is "exact length" - pair check skips.
	mustClean(t, `type X { name string @length(5) }`)
}

// ---------- @range pair ----------

func TestRangeMinExceedsMax(t *testing.T) {
	expectDiag(t, `type X { score int @range(100, 1) }`, CodeDecoratorRange)
}

func TestRangeOK(t *testing.T) {
	mustClean(t, `type X { score int @range(0, 100) }`)
}

// ---------- @multipleOf ----------

func TestMultipleOfZeroRejected(t *testing.T) {
	expectDiag(t, `type X { n int @multipleOf(0) }`, CodeDecoratorRange)
}

func TestMultipleOfNonZeroOK(t *testing.T) {
	mustClean(t, `type X { n int @multipleOf(2) }`)
	// A whole-valued float divisor is fine (folds to the int divisor).
	mustClean(t, `type X { n int @multipleOf(5.0) }`)
}

func TestMultipleOfFractionalOnIntRejected(t *testing.T) {
	// A fractional divisor can't be enforced by integer modulus, yet the
	// OpenAPI would advertise it — reject so spec and validator agree.
	expectDiag(t, `type X { n int @multipleOf(2.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, "scalar Step int @multipleOf(2.5)", CodeDecoratorTypeMismatch)
}

// ---------- @negative on unsigned ----------

func TestNegativeOnUnsignedRejected(t *testing.T) {
	// A uint is always >= 0, so the emitted `value >= 0` rejection fires
	// for every value — @negative could never pass. Reject at design time.
	expectDiag(t, `type X { count uint @negative }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { n uint32 @negative }`, CodeDecoratorTypeMismatch)
	// Caught through a named scalar over an unsigned primitive...
	expectDiag(t, "scalar Qty uint\ntype X { q Qty @negative }", CodeDecoratorTypeMismatch)
	// ...and on the scalar declaration itself.
	expectDiag(t, `scalar Qty uint @negative`, CodeDecoratorTypeMismatch)
}

func TestNegativeOnSignedAndPositiveOnUnsignedOK(t *testing.T) {
	// @negative on a signed int is fine; @positive on a uint is fine
	// (it rejects only 0, which a uint can legitimately exclude).
	mustClean(t, `type X { delta int @negative }`)
	mustClean(t, `type X { count uint @positive }`)
}

// ---------- @status ----------

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

// ---------- Duration / Size ----------

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

func TestDurationLiteralAcceptedAlways(t *testing.T) {
	// Duration literals are always OK - only the bare-int form is
	// range-checked here.
	mustClean(t, `service S {
	@timeout(5s)
	get G /g {}
}`)
}

func TestZeroSizeRejected(t *testing.T) {
	expectDiag(t, `service S {
	@maxBodySize(0)
	get G /g {}
}`, CodeDecoratorRange)
}

func TestSizeLiteralAcceptedAlways(t *testing.T) {
	mustClean(t, `service S {
	@maxBodySize(1MB)
	get G /g {}
}`)
}

// ---------- @minLength / @maxLength etc. negative ----------

func TestMinLengthNegative(t *testing.T) {
	expectDiag(t, `type X { name string @minLength(-1) }`, CodeDecoratorRange)
}

// ---------- pair ordering across decorators ----------

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
	// Strict + inclusive combos with equal endpoints define an empty
	// value set — every input fails one of the two checks. Currently a
	// warning so users can still hand-roll edge cases; codegen would
	// otherwise emit a silently-broken validator.
	expectDiag(t, `type X { v int @gt(5) @lt(5) }`, CodeBoundEmptyRange)
	expectDiag(t, `type X { v int @gte(5) @lt(5) }`, CodeBoundEmptyRange)
	expectDiag(t, `type X { v int @gt(5) @lte(5) }`, CodeBoundEmptyRange)
	// Fully-inclusive `@gte(N) @lte(N)` accepts the single value N
	// — that's a legitimate "exact match" pattern, not an empty set.
	mustClean(t, `type X { v int @gte(5) @lte(5) }`)
}

func TestMultipleOfNegativeRejected(t *testing.T) {
	// `n % -2 == 0` works in Go but the decorator intent is "multiple
	// of a positive divisor"; accepting negatives silently leads to
	// confusing validators around the dividend's sign.
	expectDiag(t, `type X { n int @multipleOf(-2) }`, CodeDecoratorRange)
}

func TestCrossFieldDuplicateRef(t *testing.T) {
	// @requiresOneOf(a, a, b) — duplicate field names get rejected
	// because the generated check would be `v.A == nil && v.A == nil`,
	// which go vet flags as a redundant boolean expression and breaks
	// `go test` for downstream projects.
	expectDiag(t, `@requiresOneOf(a, a, b)
type X { a string? b string? }`, CodeDuplicateGroupField)
}

func TestMutuallyExclusiveSingleField(t *testing.T) {
	// @mutuallyExclusive(only) with a single field — the counter
	// check `n > 1` is unreachable, so the rule never fires. Flag
	// it so the author either adds more fields or removes the
	// decorator.
	expectDiag(t, `@mutuallyExclusive(only)
type X { only string? }`, CodeMutExSingleField)
}

func TestBoundOverflowInt8(t *testing.T) {
	// Bound literals that exceed the field primitive's capacity are
	// rejected at semantic time so codegen never emits something
	// like `if v.X > 300` against an int8 field (300 overflows the
	// int8 range — max 127).
	expectDiag(t, `type X { score int8 @lte(300) }`, CodeBoundOverflow)
	expectDiag(t, `type X { neg int8 @gte(-200) }`, CodeBoundOverflow)
	expectDiag(t, `type X { u uint @lt(-1) }`, CodeBoundOverflow)
	// Within range — OK.
	mustClean(t, `type X { score int8 @lte(127) @gte(-128) }`)
	mustClean(t, `type X { u uint8 @range(0, 255) }`)
}

func TestMinExceedsMax(t *testing.T) {
	expectDiag(t, `type X { score int @gte(100) @lte(10) }`, CodeDecoratorRange)
}

func TestFractionalBoundOnIntRejected(t *testing.T) {
	// A fractional float bound on an integer field renders to a Go
	// float literal compared against an int, which fails to compile
	// ("constant 0.5 truncated to integer"). Reject it at design time
	// across @gt/@gte/@lt/@lte and both @range positions.
	d := expectDiag(t, `type X { count int @gte(0.5) }`, CodeDecoratorTypeMismatch)
	expectMessage(t, d, "whole number", "count")
	expectDiag(t, `type X { count int @lte(10.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @gt(0.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @lt(9.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @range(0.5, 10) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type X { count int @range(0, 10.5) }`, CodeDecoratorTypeMismatch)
	// A field typed through a local integer scalar resolves to the same
	// primitive and is rejected too.
	expectDiag(t, `scalar Count int
type X { n Count @gte(0.5) }`, CodeDecoratorTypeMismatch)
}

func TestFractionalBoundOnScalarRejected(t *testing.T) {
	// A scalar's bounds are inherited into every field that uses it, so
	// a fractional bound on an integer scalar is caught on the scalar
	// declaration itself.
	expectDiag(t, `scalar Half int @gte(0.5)`, CodeDecoratorTypeMismatch)
	expectDiag(t, `scalar Half uint8 @range(0.5, 9)`, CodeDecoratorTypeMismatch)
}

func TestFractionalBoundOnFloatOK(t *testing.T) {
	// Float-typed targets are exactly what fractional bounds are for.
	mustClean(t, `type X { ratio float64 @gte(0.5) @lte(1.5) }`)
	mustClean(t, `type X { ratio float32 @range(0.1, 0.9) }`)
	mustClean(t, `scalar Half float64 @gte(0.5)`)
}

func TestFloat32BoundOverflow(t *testing.T) {
	// A bound whose magnitude exceeds the float32 range (~3.4028e38) renders
	// a float32 literal that overflows and won't compile - reject at design
	// time, on fields and on float32 scalar declarations alike.
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
	// An integral float literal renders to a whole-number Go literal
	// (`1.0` → `1`), so it compiles fine and is not flagged — the check
	// targets only genuinely fractional values.
	mustClean(t, `type X { count int @gte(1.0) @lte(10.0) }`)
	mustClean(t, `type X { count int @range(0.0, 100.0) }`)
}

func TestMinMaxOnlyOneSide(t *testing.T) {
	// Solo decorator is unconstrained - pair ordering only fires when
	// both halves are present.
	mustClean(t, `type X { score int @gte(0) }`)
	mustClean(t, `type X { name string @maxLength(50) }`)
}

// ---------- @nullable on T? warning ----------

func TestNullableOnOptionalIsWarning(t *testing.T) {
	expectWarning(t, `type X { name string? @nullable }`, CodeDecoratorRedundant)
}

func TestNullableOnNonOptionalOK(t *testing.T) {
	mustClean(t, `type X { name string @nullable }`)
}

// ---------- Scalar value-range ----------

func TestScalarRangeChecked(t *testing.T) {
	expectDiag(t, `scalar Score int @range(100, 1)`, CodeDecoratorRange)
}

// ---------- Helpers / nil-shape ----------

func TestNumericValue(t *testing.T) {
	if v, ok := numericValue(&ast.IntLit{Value: 7}); !ok || v != 7 {
		t.Error("int")
	}
	if v, ok := numericValue(&ast.FloatLit{Value: 1.5}); !ok || v != 1.5 {
		t.Error("float")
	}
	if _, ok := numericValue(&ast.StringLit{}); ok {
		t.Error("string should not match")
	}
}

func TestSingleNumericArgMissing(t *testing.T) {
	v, _, ok := singleNumericArg([]*ast.Decorator{{Name: "min"}}, "min")
	if ok {
		t.Errorf("decorator with no args should return ok=false, got %v", v)
	}
	// Wrong-shape value also returns false.
	_, _, ok = singleNumericArg([]*ast.Decorator{
		{Name: "min", Args: []*ast.DecoratorArg{{Value: &ast.StringLit{}}}},
	}, "min")
	if ok {
		t.Error("string arg should return ok=false")
	}
	// Decorator absent.
	_, _, ok = singleNumericArg([]*ast.Decorator{nil, {Name: "max"}}, "min")
	if ok {
		t.Error("absent decorator should return ok=false")
	}
}

func TestRangesNilDecoratorTolerated(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.checkDecoratorRanges([]*ast.Decorator{nil})
	a.checkBodyRanges([]ast.TypeMember{
		// Mixin members are skipped.
		&ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Other"}}}},
	}, nil)
	if len(a.diags) != 0 {
		t.Errorf("expected no diags, got %v", a.diags)
	}
}

// TestRangeHelpersTolerateBadShape exercises every helper's defensive
// early returns. The args pass would normally short-circuit before
// these helpers are called with invalid shapes; we hit them directly
// so the coverage gate stays at 100%.
func TestRangeHelpersTolerateBadShape(t *testing.T) {
	a := &analyzer{pkg: &Package{}}

	// Wrong arity: each helper returns early.
	a.checkPairArgs(&ast.Decorator{Name: "length"}) // 0 args
	a.checkMultipleOf(&ast.Decorator{Name: "multipleOf"})
	a.checkHTTPStatus(&ast.Decorator{Name: "status"})
	a.checkPositiveDuration(&ast.Decorator{Name: "timeout"})
	a.checkPositiveSize(&ast.Decorator{Name: "maxBodySize"})
	a.checkNonNegativeInt(&ast.Decorator{Name: "minLength"})

	// Non-numeric value: helpers also return early.
	stringArg := []*ast.DecoratorArg{{Value: &ast.StringLit{}}}
	a.checkPairArgs(&ast.Decorator{Name: "length", Args: append(stringArg, &ast.DecoratorArg{Value: &ast.StringLit{}})})
	a.checkMultipleOf(&ast.Decorator{Name: "multipleOf", Args: stringArg})
	a.checkHTTPStatus(&ast.Decorator{Name: "status", Args: stringArg})

	if len(a.diags) != 0 {
		t.Errorf("defensive helpers should not diag on bad shape, got %v", a.diags)
	}
}

// ---------- @uniqueItems comparability ----------

func TestUniqueItemsNonComparableRejected(t *testing.T) {
	// Element not usable as a Go map key → reject (else non-compiling Go
	// / runtime hash panic / spec-says-unique-but-validator-drops).
	expectDiag(t, `type T { twoD string[][] @uniqueItems }`, CodeDecoratorTypeMismatch)
	expectDiag(t, `type T { a any[] @uniqueItems }`, CodeDecoratorTypeMismatch)
	expectDiag(t, "type NC { rows string[] }\ntype T { s NC[] @uniqueItems }", CodeDecoratorTypeMismatch)
	expectDiag(t, "type Page<X> { items X[]  total int }\ntype T { p Page<string>[] @uniqueItems }", CodeDecoratorTypeMismatch)
	// Comparable only after substituting the type argument: Pair<bytes>
	// holds `bytes` fields, so the dedupe map[Pair[[]byte]] won't compile.
	expectDiag(t, "type Pair<X> { a X  b X }\ntype T { ps Pair<bytes>[] @uniqueItems }", CodeDecoratorTypeMismatch)
}

func TestUniqueItemsComparableOK(t *testing.T) {
	// Comparable element types stay legal.
	mustClean(t, `type T { tags string[] @uniqueItems  nums int[] @uniqueItems }`)
	mustClean(t, "scalar Tag string @minLength(1)\ntype T { tags Tag[] @uniqueItems }")
	mustClean(t, "enum Color { Red  Blue }\ntype T { cs Color[] @uniqueItems }")
	mustClean(t, "type Pt { x int  y int }\ntype T { pts Pt[] @uniqueItems }")
	// A generic instance over a comparable argument stays legal.
	mustClean(t, "type Pair<X> { a X  b X }\ntype T { ps Pair<int>[] @uniqueItems }")
}

// ---------- map key comparability ----------

func TestMapKeyNotMarshalableRejected(t *testing.T) {
	// A generic type-parameter key lowers to `map[K any]` — invalid Go.
	expectDiag(t, "type Item { id int }\ntype Index<K> { byKey map<K, Item> }", CodeMapKeyType)
	// A struct with a slice field is not comparable, so `map[Item]...` fails.
	expectDiag(t, "type Item { tags string[] }\ntype Bad { m map<Item, string> }", CodeMapKeyType)
	// An all-comparable struct key COMPILES but json.Marshal can't serialise
	// it (JSON object keys are strings), so it is rejected too.
	expectDiag(t, "type Key { id int  region string }\ntype Bag { m map<Key, string> }", CodeMapKeyType)
	// A bool / float key is comparable and compiles, but json.Marshal rejects
	// it at runtime ("unsupported type"), so it is rejected at design time.
	expectDiag(t, "type V { x int }\ntype Bag { m map<bool, V> }", CodeMapKeyType)
	expectDiag(t, "type V { x int }\ntype Bag { m map<float64, V> }", CodeMapKeyType)
	// A scalar over a bool / float primitive resolves to the same
	// non-marshalable key and is rejected.
	expectDiag(t, "scalar Flag bool\ntype V { x int }\ntype Bag { m map<Flag, V> }", CodeMapKeyType)
	expectDiag(t, "scalar Ratio float64\ntype V { x int }\ntype Bag { m map<Ratio, V> }", CodeMapKeyType)
}

func TestMapKeyMarshalableOK(t *testing.T) {
	mustClean(t, "type Item { id int }\ntype Bag { m map<string, Item> }")
	// A string- / int-backed scalar and an enum are valid string-keys.
	mustClean(t, "scalar UserID int @gte(1)\ntype Item { id int }\ntype Bag { m map<UserID, Item> }")
	mustClean(t, "enum Color { Red  Blue }\ntype Item { id int }\ntype Bag { m map<Color, Item> }")
	// Nested maps with string / int keys.
	mustClean(t, "type V { x int }\ntype Bag { m map<string, map<int, V>> }")
}
