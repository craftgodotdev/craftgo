package format

import (
	"strings"
	"testing"
)

// Format prints every literal as it is written: escapes, characters Go would
// escape, raw strings and float spellings.
func TestFormatKeepsLiteralsAsWritten(t *testing.T) {
	for _, c := range []struct {
		name, field string
	}{
		{"tab escape", `a string @doc("tab \u{9} here")`},
		{"line separator escape", `a string @doc("ls \u{2028} x")`},
		{"zero-width space", "a string @doc(\"zero\u200bwidth\")"},
		{"raw pattern", "a string @pattern(`^\\d+$`)"},
		{"large float", `a float64 @lte(1234567.5)`},
		{"small float", `a float64 @gte(0.00001)`},
		{"negative float", `a float64 @gte(-0.5)`},
		{"float with a zero fraction", `a float64 @lte(1000000.0)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "package app\n\ntype D {\n\t" + c.field + "\n}\n"
			formatExact(t, src, src)
		})
	}
}

// Format prints enum string values as they are written.
func TestFormatKeepsEnumValuesAsWritten(t *testing.T) {
	src := "package app\n\nenum Bell {\n\tRing = \"\\u{2028}\"\n\tQuiet = \"zero\u200bwidth\"\n}\n"
	out := formatStable(t, src)
	for _, want := range []string{`Ring  = "\u{2028}"`, "Quiet = \"zero\u200bwidth\""} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
}
