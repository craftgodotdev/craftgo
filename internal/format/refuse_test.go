package format

import "testing"

// Format returns the parser diagnostic for a decorator that follows a mixin on its line.
func TestFormatReportsStrandedDecorator(t *testing.T) {
	_, diags := Format("t.craftgo", "package p\n\ntype A {\n\tuser string S @default(\"\")\n\tname string\n}\n")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic, got none")
	}
}
