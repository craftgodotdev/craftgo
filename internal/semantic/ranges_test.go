package semantic

import (
	"strings"
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
	// OpenAPI would advertise it - reject so spec and validator agree.
	expectDiag(t, `type X { n int @multipleOf(2.5) }`, CodeDecoratorTypeMismatch)
	expectDiag(t, "scalar Step int @multipleOf(2.5)", CodeDecoratorTypeMismatch)
}

// ---------- @negative on unsigned ----------

func TestNegativeOnUnsignedRejected(t *testing.T) {
	// A uint is always >= 0, so the emitted `value >= 0` rejection fires
	// for every value - @negative could never pass. Reject at design time.
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

func TestPositiveDurationLiteralAccepted(t *testing.T) {
	mustClean(t, `service S {
	@timeout(5s)
	get G /g {}
}`)
}

// TestZeroDurationLiteralRejected pins the suffixed form to the same rule
// as the bare int: `@timeout(0s)` cancels nothing either.
func TestZeroDurationLiteralRejected(t *testing.T) {
	expectDiag(t, `service S {
	@timeout(0s)
	get G /g {}
}`, CodeDecoratorRange)
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

// TestZeroSizeLiteralRejected pins the suffixed form to the same rule as
// the bare count: a 0-byte cap reads as "no cap" and emits no check.
func TestZeroSizeLiteralRejected(t *testing.T) {
	expectDiag(t, `service S {
	@maxBodySize(0B)
	get G /g {}
}`, CodeDecoratorRange)
}

// TestOverflowingSizeLiteralRejected covers a count past int64: the
// emitters would enforce a wrapped or saturated cap nobody wrote.
func TestOverflowingSizeLiteralRejected(t *testing.T) {
	d := expectDiag(t, `service S {
	@maxBodySize(99999999999999999999GB)
	get G /g {}
}`, CodeDecoratorRange)
	expectMessage(t, d, "is not a byte size")
}

// TestZeroMaxSizeRejected covers the field-level size decorator, which
// shares checkPositiveSize with the method-level one.
func TestZeroMaxSizeRejected(t *testing.T) {
	expectDiag(t, `type Req { avatar file @maxSize(0MB) }`, CodeDecoratorRange)
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
	// value set - every input fails one of the two checks. Currently a
	// warning so users can still hand-roll edge cases; codegen would
	// otherwise emit a silently-broken validator.
	expectDiag(t, `type X { v int @gt(5) @lt(5) }`, CodeBoundEmptyRange)
	expectDiag(t, `type X { v int @gte(5) @lt(5) }`, CodeBoundEmptyRange)
	expectDiag(t, `type X { v int @gt(5) @lte(5) }`, CodeBoundEmptyRange)
	// Fully-inclusive `@gte(N) @lte(N)` accepts the single value N
	// - that's a legitimate "exact match" pattern, not an empty set.
	mustClean(t, `type X { v int @gte(5) @lte(5) }`)
}

func TestMultipleOfNegativeRejected(t *testing.T) {
	// `n % -2 == 0` works in Go but the decorator intent is "multiple
	// of a positive divisor"; accepting negatives silently leads to
	// confusing validators around the dividend's sign.
	expectDiag(t, `type X { n int @multipleOf(-2) }`, CodeDecoratorRange)
}

func TestCrossFieldDuplicateRef(t *testing.T) {
	// @requiresOneOf(a, a, b) - duplicate field names get rejected
	// because the generated check would be `v.A == nil && v.A == nil`,
	// which go vet flags as a redundant boolean expression and breaks
	// `go test` for downstream projects.
	expectDiag(t, `@requiresOneOf(a, a, b)
type X { a string? b string? }`, CodeDuplicateGroupField)
}

func TestMutuallyExclusiveSingleField(t *testing.T) {
	// @mutuallyExclusive(only) with a single field - the counter
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
	// int8 range - max 127).
	expectDiag(t, `type X { score int8 @lte(300) }`, CodeBoundOverflow)
	expectDiag(t, `type X { neg int8 @gte(-200) }`, CodeBoundOverflow)
	expectDiag(t, `type X { u uint @lt(-1) }`, CodeBoundOverflow)
	// Within range - OK.
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
	// (`1.0` → `1`), so it compiles fine and is not flagged - the check
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
	a := newTestAnalyzer(&Package{})
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
	a := newTestAnalyzer(&Package{})

	// Wrong arity: each helper returns early.
	a.checkPairArgs(&ast.Decorator{Name: "length"}) // 0 args
	a.checkMultipleOf(&ast.Decorator{Name: "multipleOf"})
	a.checkHTTPStatus(&ast.Decorator{Name: "status"})
	a.checkPositiveDuration(&ast.Decorator{Name: "timeout"})
	a.checkPositiveSize(&ast.Decorator{Name: "maxBodySize"})
	a.checkNonNegativeInt(&ast.Decorator{Name: "minLength"})

	// Non-numeric value: helpers also return early.
	StringArg := []*ast.DecoratorArg{{Value: &ast.StringLit{}}}
	a.checkPairArgs(&ast.Decorator{Name: "length", Args: append(StringArg, &ast.DecoratorArg{Value: &ast.StringLit{}})})
	a.checkMultipleOf(&ast.Decorator{Name: "multipleOf", Args: StringArg})
	a.checkHTTPStatus(&ast.Decorator{Name: "status", Args: StringArg})

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
	// A generic type-parameter key lowers to `map[K any]` - invalid Go.
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

// `@lt(0)` on an unsigned field demands "value < 0", which no uint* can
// satisfy - the desugared spelling of `@negative`, which is already
// rejected. The capacity guard misses it (0 is itself in range).
func TestUnsignedLtZeroRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type T { c uint16 @lt(0) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected @lt(0)-on-unsigned rejection; got %v", codes(diags))
	}
}

// `@lt(N)` with N>0 on unsigned is satisfiable (0..N-1) and must NOT be
// rejected - the guard targets only the empty predicate.
func TestUnsignedLtPositiveClean(t *testing.T) {
	mustClean(t, `type T { c uint16 @lt(10) }`)
}

// `@lt(0.0)` is the same always-false predicate as `@lt(0)`; the float
// spelling must be rejected on unsigned too, not silently emit `value >= 0`.
func TestUnsignedLtZeroFloatRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type T { c uint16 @lt(0.0) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected @lt(0.0)-on-unsigned rejection; got %v", codes(diags))
	}
}

// A positive float bound on unsigned is satisfiable and must stay clean -
// argIsZero must not over-fire on non-zero floats.
func TestUnsignedLtPositiveFloatClean(t *testing.T) {
	mustClean(t, `type T { c uint16 @lt(10.0) }`)
}

// An integral float bound above the target's capacity must be rejected. The
// old int64() round-trip saturated for values beyond MaxInt64, so the
// integrality test failed, the capacity check was skipped, and codegen emitted
// a constant that overflows uint64.
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

// An integral float bound within the target's range is valid and must stay
// clean - isIntegralFloat must not trigger a false overflow.
func TestFloatBoundInRangeClean(t *testing.T) {
	mustClean(t, `type T { c uint64 @lte(18000000000000000000.0) }`)
}

// The float-zero rejection also fires on a CROSS-PACKAGE unsigned scalar,
// through the project twin ([refResolver.checkScalarBoundContradictions]).
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

// A contradictory bound on a CROSS-PACKAGE unsigned scalar must be caught
// (the per-package pass can't resolve the foreign scalar's primitive).
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

// A numeric bound that overflows the scalar's primitive must be rejected
// at the scalar DECLARATION, matching the field path (else codegen emits
// non-compiling Go like `if uint8(v) > 300`).
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

// @lt(0) / @negative on an unsigned scalar declaration is an always-false
// validator - reject like the field path does.
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

// An in-range scalar bound stays clean (the capacity check must not over-fire).
func TestScalarDeclBoundInRangeClean(t *testing.T) {
	diags := analyzeOneFile(t, "package p\nscalar X uint8 @lte(200) @gte(1)\n")
	if hasDiagContaining(diags, "exceeds") {
		t.Errorf("in-range scalar bound wrongly rejected: %v", diags)
	}
}

// The 1-arg exact-length form `@length(-1)` must be rejected (it otherwise
// emits an always-true reject while OpenAPI advertises no constraint).
func TestNegativeExactLengthRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype T { a string @length(-1) }\n")
	if !hasDiagContaining(diags, "exact length must be") {
		t.Errorf("expected @length(-1) reject, got: %v", diags)
	}
}

// An out-of-capacity INTEGRAL-FLOAT bound must be rejected like the int form.
func TestIntegralFloatBoundCapacityRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype T { a int8 @gte(300.0) }\n")
	if !hasDiagContaining(diags, "exceeds") {
		t.Errorf("expected integral-float capacity reject, got: %v", diags)
	}
}

// W2: a scalar declaration with contradictory pair bounds is rejected
// (pair-ordering now runs on scalar decls, not only fields).
func TestScalarDeclPairOrderingRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar Score int @gte(100) @lte(10)\n",
		"package p\nscalar Name string @minLength(10) @maxLength(5)\n",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "must be ≥") && !hasDiagContaining(diags, "must be ≤") {
			t.Errorf("expected scalar pair-ordering reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}
