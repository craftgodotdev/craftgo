package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
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

// parseFiles parses each source as the file test<i>.craftgo; a source
// without a `package` clause is a file of package test.
func parseFiles(t *testing.T, sources ...string) []*ast.File {
	t.Helper()
	var files []*ast.File
	for i, src := range sources {
		p := parser.New("test"+itoa(i)+".craftgo", src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse error in source %d: %v", i, d)
		}
		if f.Package == nil {
			f.Package = &ast.PackageDecl{Name: "test"}
		}
		files = append(files, f)
	}
	return files
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	var sb strings.Builder
	if n < 0 {
		sb.WriteByte('-')
		n = -n
	}
	var stack []byte
	for n > 0 {
		stack = append(stack, digits[n%10])
		n /= 10
	}
	for i := len(stack) - 1; i >= 0; i-- {
		sb.WriteByte(stack[i])
	}
	return sb.String()
}

func mustClean(t *testing.T, sources ...string) *Package {
	t.Helper()
	pkg, diags := Analyze(parseFiles(t, sources...))
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return pkg
}

// newTestAnalyzer returns an analyzer whose project holds pkg alone.
func newTestAnalyzer(pkg *Package) *analyzer {
	return &analyzer{pkg: pkg, proj: &Project{Packages: map[string]*Package{pkg.Name: pkg}}}
}

func codes(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func findCode(diags []Diagnostic, code string) *Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}

// parseFileMap parses each file of files, keyed by name.
func parseFileMap(t *testing.T, files map[string]string) []*ast.File {
	t.Helper()
	out := make([]*ast.File, 0, len(files))
	for name, src := range files {
		p := parser.New(name, src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse %s: %v", name, d)
		}
		out = append(out, f)
	}
	return out
}

// projectFixture writes src (design-relative path → content) under a temp root and parses it.
func projectFixture(t *testing.T, src map[string]string) (string, []*ast.File) {
	t.Helper()
	root := t.TempDir()
	var files []*ast.File
	for rel, content := range src {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		p := parser.New(full, content)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse error in %s: %v", rel, d)
		}
		files = append(files, f)
	}
	return root, files
}

// hasCode reports whether any diagnostic carries code.
func hasCode(diags []Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
