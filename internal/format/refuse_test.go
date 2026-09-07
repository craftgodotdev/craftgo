package format

import "testing"

// Format surfaces the parser diagnostic for a decorator stranded after a
// mixin, so `craftgo fmt` and the editor leave the file alone instead of
// moving the decorator to the next field.
func TestFormatReportsStrandedDecorator(t *testing.T) {
	_, diags := Format("t.craftgo", "package p\n\ntype A {\n\tuser string S @default(\"\")\n\tname string\n}\n")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic, got none")
	}
}
