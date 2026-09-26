package semantic

import (
	"testing"
)

// Bounds that no value of the type meets are an error, once, however the
// design spreads them: on the integer grid, at a float's width, against the
// type's range, across a field and its scalar, or against a @multipleOf.
func TestBoundsLeavingNoValueRejected(t *testing.T) {
	for _, c := range []struct{ src, code, msg string }{
		{"type X { v int @gt(4) @lt(5) }", CodeBoundEmptyRange,
			"@lt(5) contradicts @gt(4): no int is both > 4 and < 5"},
		{"type X { v uint @positive @lt(1) }", CodeBoundEmptyRange,
			"@lt(1) contradicts @positive: no uint is both > 0 and < 1"},
		{"type X { v int @gt(9223372036854775806) @lt(9223372036854775807) }", CodeBoundEmptyRange,
			"no int is both > 9223372036854775806 and < 9223372036854775807"},
		{"type X { v uint8 @gt(255) }", CodeBoundEmptyRange,
			"@gt(255) leaves no value: no uint8 is > 255"},
		{"type X { v uint64 @gt(18446744073709551615.0) }", CodeBoundEmptyRange,
			"@gt(18446744073709551615.0) leaves no value: no uint64 is > 18446744073709551615.0"},
		{"type X { v int64 @lt(-9223372036854775808.0) }", CodeBoundEmptyRange,
			"no int64 is < -9223372036854775808.0"},
		{"type X { v int8 @lt(-128) }", CodeBoundEmptyRange,
			"@lt(-128) leaves no value: no int8 is < -128"},
		{"type X { v float64 @gt(0.1) @lt(0.10000000000000001) }", CodeBoundEmptyRange,
			"@lt(0.10000000000000001) contradicts @gt(0.1): no float64 is both > 0.1 and < 0.10000000000000001"},
		{"type X { v float32 @gt(1) @lt(1.0000001) }", CodeBoundEmptyRange,
			"no float32 is both > 1 and < 1.0000001"},
		{"type X { v float32 @gt(340282346638528859811704183484516925440.0) }", CodeBoundEmptyRange,
			"no float32 is > 340282346638528859811704183484516925440.0"},
		{"type X { v int @gte(1) @lte(4) @multipleOf(5) }", CodeBoundEmptyRange,
			"@multipleOf(5) leaves no value: no int ≥ 1 and ≤ 4 is a multiple of 5"},
	} {
		d := expectError(t, c.src, c.code)
		expectMessage(t, d, c.msg)
		expectCodeCount(t, c.src, c.code, 1)
	}
}

// A field's bound that contradicts its scalar's is reported at the field and
// points at the scalar's decorator; the scalar alone is fine.
func TestFieldBoundContradictingItsScalar(t *testing.T) {
	for _, c := range []struct{ src, code, msg string }{
		{"scalar Pos int @positive\ntype X { n Pos @negative }", CodeBoundEmptyRange,
			"@negative contradicts @positive of scalar Pos: no value is both > 0 and < 0"},
		{"scalar Code string @minLength(5)\ntype X { c Code @maxLength(3) }", CodeDecoratorRange,
			"@maxLength(3) contradicts @minLength(5) of scalar Code: no length is both ≥ 5 and ≤ 3"},
		{"scalar Low int @lt(10)\ntype X { n Low @gt(9) }", CodeBoundEmptyRange,
			"@gt(9) contradicts @lt(10) of scalar Low: no int is both > 9 and < 10"},
		{"scalar Step int @multipleOf(5)\ntype X { n Step @range(1, 4) }", CodeBoundEmptyRange,
			"no int ≥ 1 and ≤ 4 is a multiple of 5"},
	} {
		_, diags := Analyze(parseFiles(t, c.src))
		d := findCode(diags, c.code)
		if d == nil {
			t.Fatalf("%s: expected %s; got %v", c.src, c.code, codes(diags))
		}
		expectMessage(t, d, c.msg)
		if d.Pos.Line != 2 {
			t.Errorf("%s: reported at line %d, want the field's line 2", c.src, d.Pos.Line)
		}
		if len(d.Related) != 1 || d.Related[0].Pos.Line != 1 {
			t.Errorf("%s: want one related position on the scalar's line 1, got %+v", c.src, d.Related)
		}
		expectCodeCount(t, c.src, c.code, 1)
	}
}

// A scalar whose own bounds leave no value is reported at the scalar only,
// not again at each field of its type.
func TestScalarLeavingNoValueReportedOnce(t *testing.T) {
	expectCodeCount(t, "scalar Bad int @gt(4) @lt(5)\ntype X { a Bad  b Bad? }", CodeBoundEmptyRange, 1)
}

// Bounds that leave a value, however few, are clean.
func TestBoundsLeavingAValueClean(t *testing.T) {
	mustClean(t, `type X {
    a int     @gt(4) @lt(6)
    b uint    @positive @lte(1)
    c uint8   @gte(255)
    d int64   @lte(-9223372036854775808.0)
    e float64 @gt(0.1) @lt(0.2)
    f float32 @gt(1) @lt(1.0000002)
    g int     @gte(1) @lte(5) @multipleOf(5)
    h int8    @multipleOf(100)
}`)
	mustClean(t, "scalar Pos int @positive\ntype X { n Pos @lte(1) }")
	mustClean(t, "scalar Step int @multipleOf(5)\ntype X { n Step @range(1, 5) }")
}
