package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// expectClean fails the test when src produces any diagnostic.
func expectClean(t *testing.T, src string) *Package {
	t.Helper()
	return mustClean(t, src)
}

// expectDiag returns the first diagnostic with code that src produces, failing when there is none.
func expectDiag(t *testing.T, src, code string) *Diagnostic {
	t.Helper()
	_, diags := Analyze(parseFiles(t, src))
	d := findCode(diags, code)
	if d == nil {
		t.Fatalf("expected diagnostic %s; got %v", code, codes(diags))
	}
	return d
}

// expectWarning is expectDiag that also requires warning severity.
func expectWarning(t *testing.T, src, code string) *Diagnostic {
	t.Helper()
	d := expectDiag(t, src, code)
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity for %s, got %v", code, d.Severity)
	}
	return d
}

// expectError is expectDiag that also requires error severity.
func expectError(t *testing.T, src, code string) *Diagnostic {
	t.Helper()
	d := expectDiag(t, src, code)
	if d.Severity != lexer.SeverityError {
		t.Errorf("expected error severity for %s, got %v", code, d.Severity)
	}
	return d
}

// expectMessage fails unless d's message contains every substring.
func expectMessage(t *testing.T, d *Diagnostic, substrs ...string) {
	t.Helper()
	if d == nil {
		t.Fatal("expectMessage called with nil diagnostic")
	}
	for _, s := range substrs {
		if !strings.Contains(d.Msg, s) {
			t.Errorf("diagnostic message must contain %q; got %q", s, d.Msg)
		}
	}
}

// expectCodeCount fails unless src produces exactly want diagnostics with code.
func expectCodeCount(t *testing.T, src, code string, want int) {
	t.Helper()
	_, diags := Analyze(parseFiles(t, src))
	got := 0
	for _, d := range diags {
		if d.Code == code {
			got++
		}
	}
	if got != want {
		t.Errorf("expected %d diagnostics with code %s, got %d (%v)", want, code, got, codes(diags))
	}
}

// expectNoCode fails if src produces a diagnostic with code.
func expectNoCode(t *testing.T, src, code string) {
	t.Helper()
	_, diags := Analyze(parseFiles(t, src))
	for _, d := range diags {
		if d.Code == code {
			t.Errorf("did not expect %s diagnostic; got %q", code, d.Msg)
		}
	}
}

// expectMsg returns the first diagnostic of sources whose message contains substr.
func expectMsg(t *testing.T, substr string, sources ...string) *Diagnostic {
	t.Helper()
	_, diags := Analyze(parseFiles(t, sources...))
	for i := range diags {
		if strings.Contains(diags[i].Msg, substr) {
			return &diags[i]
		}
	}
	t.Fatalf("no diagnostic contained %q; got %v", substr, diags)
	return nil
}

// expectNoMsg fails if a diagnostic of sources mentions substr.
func expectNoMsg(t *testing.T, substr string, sources ...string) {
	t.Helper()
	_, diags := Analyze(parseFiles(t, sources...))
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			t.Errorf("did not expect diagnostic mentioning %q; got %q", substr, d.Msg)
		}
	}
}

// expectNoDiags fails the test when diags is not empty.
func expectNoDiags(t *testing.T, diags []Diagnostic) {
	t.Helper()
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

// analyzeOneFile runs a single-package analysis and returns the diagnostics.
func analyzeOneFile(t *testing.T, src string) []Diagnostic {
	t.Helper()
	files := parseFiles(t, src)
	_, diags := Analyze(files)
	return diags
}

func hasDiagContaining(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			return true
		}
	}
	return false
}
