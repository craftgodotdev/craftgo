package parser

import (
	"math"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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

// An enum value outside the int64 range is an error, as it is in a decorator.
func TestEnumIntOutOfRangeRejected(t *testing.T) {
	for _, value := range []string{"99999999999999999999", "9223372036854775808", "-9223372036854775809"} {
		p := New("t.craftgo", "enum E {\n\tA = "+value+"\n}\n")
		p.Parse()
		if d := p.Diagnostics(); len(d) != 1 || !strings.Contains(d[0].Msg, "integer literal "+value+" is outside the signed 64-bit range") {
			t.Errorf("A = %s: diagnostics %v, want one out-of-range error", value, d)
		}
	}
	for value, want := range map[string]int64{"9223372036854775807": math.MaxInt64, "-9223372036854775808": math.MinInt64} {
		p := New("t.craftgo", "enum E {\n\tA = "+value+"\n}\n")
		f := p.Parse()
		if d := p.Diagnostics(); len(d) != 0 {
			t.Fatalf("A = %s: %v", value, d)
		}
		if got := f.Decls[0].(*ast.EnumDecl).EnumValues()[0].IntValue; got != want {
			t.Errorf("A = %s parsed as %d", value, got)
		}
	}
}
