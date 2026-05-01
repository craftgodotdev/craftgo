package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// ---------- @length pair ----------

func TestLengthMinExceedsMax(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @length(20, 5) }`))
	d := findCode(diags, CodeDecoratorRange)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "min") || !strings.Contains(d.Msg, "max") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestLengthNegativeMin(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @length(-1, 5) }`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestLengthSingleArgOK(t *testing.T) {
	// @length(5) is "exact length" — pair check skips.
	mustClean(t, `type X { name string @length(5) }`)
}

// ---------- @range pair ----------

func TestRangeMinExceedsMax(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { score int @range(100, 1) }`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestRangeOK(t *testing.T) {
	mustClean(t, `type X { score int @range(0, 100) }`)
}

// ---------- @multipleOf ----------

func TestMultipleOfZeroRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { n int @multipleOf(0) }`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMultipleOfNonZeroOK(t *testing.T) {
	mustClean(t, `type X { n int @multipleOf(2) }`)
}

// ---------- @status ----------

func TestStatusOutOfRange(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@status(99)
	get GetUser /u {}
}`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestStatusTooHigh(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@status(600)
	get GetUser /u {}
}`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestStatusValidOK(t *testing.T) {
	mustClean(t, `service S {
	@status(201)
	post Create /c {}
}`)
}

// ---------- Duration / Size ----------

func TestZeroDurationRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@timeout(0)
	get G /g {}
}`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestNegativeDurationRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@timeout(-1)
	get G /g {}
}`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestDurationLiteralAcceptedAlways(t *testing.T) {
	// Duration literals are always OK — only the bare-int form is
	// range-checked here.
	mustClean(t, `service S {
	@timeout(5s)
	get G /g {}
}`)
}

func TestZeroSizeRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
	@maxBodySize(0)
	get G /g {}
}`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestSizeLiteralAcceptedAlways(t *testing.T) {
	mustClean(t, `service S {
	@maxBodySize(1MB)
	get G /g {}
}`)
}

// ---------- @minLength / @maxLength etc. negative ----------

func TestMinLengthNegative(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @minLength(-1) }`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- pair ordering across decorators ----------

func TestMinLengthExceedsMaxLength(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @minLength(10) @maxLength(5) }`))
	d := findCode(diags, CodeDecoratorRange)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to @minLength, got %+v", d.Related)
	}
}

func TestMinItemsExceedsMaxItems(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { tags string[] @minItems(10) @maxItems(2) }`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMinExceedsMax(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { score int @min(100) @max(10) }`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMinMaxOnlyOneSide(t *testing.T) {
	// Solo decorator is unconstrained — pair ordering only fires when
	// both halves are present.
	mustClean(t, `type X { score int @min(0) }`)
	mustClean(t, `type X { name string @maxLength(50) }`)
}

// ---------- @nullable on T? warning ----------

func TestNullableOnOptionalIsWarning(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string? @nullable }`))
	d := findCode(diags, CodeDecoratorRedundant)
	if d == nil {
		t.Fatalf("expected redundant warning, got %v", codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected severity warning, got %v", d.Severity)
	}
}

func TestNullableOnNonOptionalOK(t *testing.T) {
	mustClean(t, `type X { name string @nullable }`)
}

// ---------- Scalar value-range ----------

func TestScalarRangeChecked(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `scalar Score int @range(100, 1)`))
	if findCode(diags, CodeDecoratorRange) == nil {
		t.Fatalf("got %v", codes(diags))
	}
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
