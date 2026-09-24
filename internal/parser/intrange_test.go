package parser

import "testing"

// TestIntLiteralOutOfRangeRejected pins that an integer literal outside the
// int64 range is an error.
func TestIntLiteralOutOfRangeRejected(t *testing.T) {
	outOfRange := []string{
		`type X { a uint64 @lte(18446744073709551615) }`, // MaxUint64
		`type X { a uint64 @gte(9223372036854775808) }`,  // MaxInt64 + 1
		`type X { a int64 @gte(-9223372036854775809) }`,  // MinInt64 - 1
	}
	for _, src := range outOfRange {
		p := New("t.craftgo", src)
		p.Parse()
		if len(p.Diagnostics()) == 0 {
			t.Errorf("expected an out-of-range diagnostic for %q", src)
		}
	}

	inRange := []string{
		`type X { a int64 @lte(9223372036854775807) }`,  // MaxInt64
		`type X { a int64 @gte(-9223372036854775808) }`, // MinInt64
		`type X { a int @gte(0) @lte(100) }`,
	}
	for _, src := range inRange {
		p := New("t.craftgo", src)
		p.Parse()
		if d := p.Diagnostics(); len(d) != 0 {
			t.Errorf("in-range literal %q should parse cleanly, got %v", src, d)
		}
	}
}
