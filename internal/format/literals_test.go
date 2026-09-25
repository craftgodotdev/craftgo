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
		{"bell escape", `a string @doc("bell \u{7} here")`},
		{"nul escape", `a string @doc("nul \u{0} x")`},
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

// Format prints enum string values and import paths as they are written.
func TestFormatKeepsEnumValuesAndImportPathsAsWritten(t *testing.T) {
	src := "package app\n\nimport \"sh\\u{61}red\"\n\nenum Bell {\n\tRing = \"\\u{7}\"\n\tQuiet = \"zero\u200bwidth\"\n}\n"
	out := formatStable(t, src)
	for _, want := range []string{`import "sh\u{61}red"`, `Ring  = "\u{7}"`, "Quiet = \"zero\u200bwidth\""} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %s:\n%s", want, out)
		}
	}
}
