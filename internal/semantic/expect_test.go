package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// expect_test.go - focused assertion helpers for the analyser tests.
//
// The semantic analyser produces diagnostics with stable codes and
// human-readable messages; tests almost always need one of three
// shapes:
//
//   - "this DSL fragment must analyse cleanly"           → expectClean
//   - "this DSL fragment must produce diagnostic X"      → expectDiag
//   - "diagnostic X must mention substring Y in its msg" → expectMessage
//
// Helpers below collapse those patterns into single readable lines
// so the bulk of each test reads as the DSL fixture, not boilerplate
// around it. The helpers are intentionally test-only (no exported
// surface) so they don't widen the package's public API.

// expectClean fails the test when the source produces ANY diagnostic.
// Wrapper over [mustClean] kept for naming symmetry with the other
// `expect*` helpers - call sites read more uniformly when every
// assertion starts with `expect…`.
func expectClean(t *testing.T, src string) *Package {
	t.Helper()
	return mustClean(t, src)
}

// expectDiag analyses src and returns the FIRST diagnostic carrying
// the supplied code, failing the test when none is found. Lets the
// caller chain further assertions on the returned diagnostic - see
// [expectMessage] / [expectSeverity] for canned follow-ups.
func expectDiag(t *testing.T, src, code string) *Diagnostic {
	t.Helper()
	_, diags := Analyze(parseFiles(t, src))
	d := findCode(diags, code)
	if d == nil {
		t.Fatalf("expected diagnostic %s; got %v", code, codes(diags))
	}
	return d
}

// expectWarning is [expectDiag] plus a severity check. Most "soft"
// rules - name-case, field collision, enum-value collision - are
// warnings and the severity is part of the contract; collapsing the
// two assertions removes a noisy two-liner from every call site.
func expectWarning(t *testing.T, src, code string) *Diagnostic {
	t.Helper()
	d := expectDiag(t, src, code)
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity for %s, got %v", code, d.Severity)
	}
	return d
}

// expectError is the severity-asserting partner of [expectWarning].
// Used for hard rules (decl-collision, ref-unknown-symbol, ...).
func expectError(t *testing.T, src, code string) *Diagnostic {
	t.Helper()
	d := expectDiag(t, src, code)
	if d.Severity != lexer.SeverityError {
		t.Errorf("expected error severity for %s, got %v", code, d.Severity)
	}
	return d
}

// expectMessage asserts the diagnostic's message contains every
// supplied substring. Variadic so a single call covers multiple
// expectations - `expectMessage(t, d, "user_id", "UserID", "_2")`
// reads as "the warning must name both DSL spellings AND the
// suffixed Go identifier" without three separate `if` blocks.
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

// expectCodeCount asserts that exactly `want` diagnostics with the
// supplied code fire on `src`. Useful for per-duplicate-emit rules
// where N input duplicates must produce exactly N-1 diagnostics
// (one per dupe beyond the canonical first).
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

// expectNoCode asserts the code never fires on `src`. Equivalent to
// `expectClean` when the test only cares about ONE rule remaining
// silent - projects often want to confirm "no false positive on
// genuinely distinct names" without forbidding unrelated diagnostics.
func expectNoCode(t *testing.T, src, code string) {
	t.Helper()
	_, diags := Analyze(parseFiles(t, src))
	for _, d := range diags {
		if d.Code == code {
			t.Errorf("did not expect %s diagnostic; got %q", code, d.Msg)
		}
	}
}

// expectMsg analyses ONE or MORE sources and asserts at least one
// diagnostic's message contains substr. Replaces the legacy
// `diagsContain` pattern which forced callers to:
//
//	_, diags := Analyze(parseFiles(t, src))
//	if !diagsContain(diags, "substring") { t.Errorf(...) }
//
// Multi-source variant lets package-name-conflict and other multi-
// file tests stay inline. Returns the matched diagnostic so callers
// can chain further assertions.
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

// expectNoMsg is the negative form — assert NO diagnostic mentions
// substr. Useful for "this rule must NOT fire on a benign shape"
// guards where the test author knows other diagnostics may still be
// present.
func expectNoMsg(t *testing.T, substr string, sources ...string) {
	t.Helper()
	_, diags := Analyze(parseFiles(t, sources...))
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			t.Errorf("did not expect diagnostic mentioning %q; got %q", substr, d.Msg)
		}
	}
}
