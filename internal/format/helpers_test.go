package format

import (
	"testing"
)

// formatExact requires Format(src) to equal want and to format to itself.
func formatExact(t *testing.T, src, want string) {
	t.Helper()
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if out != want {
		t.Errorf("output mismatch.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
	out2, diags := Format("t.craftgo", out)
	if len(diags) > 0 {
		t.Fatalf("re-parse diagnostics: %v", diags)
	}
	if out2 != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
}
