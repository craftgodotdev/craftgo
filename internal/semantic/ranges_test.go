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
	})
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
