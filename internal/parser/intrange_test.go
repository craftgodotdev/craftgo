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
		if _, msgs := parseWithErrors(t, src); len(msgs) == 0 {
			t.Errorf("expected an out-of-range diagnostic for %q", src)
		}
	}

	inRange := []string{
		`type X { a int64 @lte(9223372036854775807) }`,  // MaxInt64
		`type X { a int64 @gte(-9223372036854775808) }`, // MinInt64
		`type X { a int @gte(0) @lte(100) }`,
	}
	for _, src := range inRange {
		if _, msgs := parseWithErrors(t, src); len(msgs) != 0 {
			t.Errorf("in-range literal %q should parse cleanly, got %v", src, msgs)
		}
	}
}

// An enum value outside the int64 range is an error, as it is in a decorator.
func TestEnumIntOutOfRangeRejected(t *testing.T) {
	for _, value := range []string{"99999999999999999999", "9223372036854775808", "-9223372036854775809"} {
		_, msgs := parseWithErrors(t, "enum E {\n\tA = "+value+"\n}\n")
		if len(msgs) != 1 || !strings.Contains(msgs[0], "integer literal "+value+" is outside the signed 64-bit range") {
			t.Errorf("A = %s: diagnostics %v, want one out-of-range error", value, msgs)
		}
	}
	for value, want := range map[string]int64{"9223372036854775807": math.MaxInt64, "-9223372036854775808": math.MinInt64} {
		ed := firstDecl[*ast.EnumDecl](t, "enum E {\n\tA = "+value+"\n}\n")
		if got := ed.EnumValues()[0].IntValue; got != want {
			t.Errorf("A = %s parsed as %d", value, got)
		}
	}
}
