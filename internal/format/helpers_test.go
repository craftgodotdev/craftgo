package format

import (
	"testing"
)

// formatStable formats src, failing on a diagnostic or when the output does
// not format to itself, and returns the output.
func formatStable(t *testing.T, src string) string {
	t.Helper()
	out, diags := Format("t.craftgo", src)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	out2, diags := Format("t.craftgo", out)
	if len(diags) > 0 {
		t.Fatalf("formatted output failed to re-parse: %v\n%s", diags, out)
	}
	if out2 != out {
		t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
	return out
}

// formatExact requires Format(src) to equal want and to format to itself.
func formatExact(t *testing.T, src, want string) {
	t.Helper()
	if out := formatStable(t, src); out != want {
		t.Errorf("output mismatch.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}
