package designopts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// tree writes files under a fresh root and returns it. Keys are paths
// relative to the root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// unreadable makes dir unreadable, skipping the test where it cannot -
// running as root defeats the mode bits.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Skipf("cannot make %s unreadable: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("directory is still readable (running as root?)")
	}
}

// names returns the base names of paths, which is what a test asserts on.
func names(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// THE REGRESSION. An unreadable directory must not truncate the list: a
// design file AFTER it in walk order is what the editor loses, and losing
// it makes the editor report unknown-symbol errors for types that exist.
//
// `bbb` sorts between `aaa` and `ccc`, so a walk that stops rather than
// skips finds a.craftgo and never reaches c.craftgo.
func TestFilesBestEffortSkipsAnUnreadableDirectoryAndKeepsGoing(t *testing.T) {
	root := tree(t, map[string]string{
		"aaa/a.craftgo": "package aaa\n",
		"bbb/b.craftgo": "package bbb\n",
		"ccc/c.craftgo": "package ccc\n",
	})
	unreadable(t, filepath.Join(root, "bbb"))

	got := names(FilesBestEffort(root))
	want := []string{"a.craftgo", "c.craftgo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilesBestEffort = %v, want %v - the file after the unreadable directory must still be found", got, want)
	}
}

// The other policy: a tool that generates from a design must refuse a
// design it could only half read, and must not hand back the half.
func TestFilesRefusesAnUnreadableDirectoryAndReturnsNoPaths(t *testing.T) {
	root := tree(t, map[string]string{
		"aaa/a.craftgo": "package aaa\n",
		"bbb/b.craftgo": "package bbb\n",
		"ccc/c.craftgo": "package ccc\n",
	})
	unreadable(t, filepath.Join(root, "bbb"))

	paths, err := Files(root)
	if err == nil {
		t.Fatal("a design that cannot be fully read must fail the call")
	}
	if paths != nil {
		t.Errorf("paths = %v, want nil - a partial list is the input that lets a tool generate from half a design", names(paths))
	}
}

// Both policies agree on a tree they can read.
func TestBothPoliciesAgreeOnAReadableTree(t *testing.T) {
	root := tree(t, map[string]string{
		"aaa/a.craftgo": "package aaa\n",
		"bbb/b.cg":      "package bbb\n",
		"notes.md":      "not a design file\n",
	})
	strict, err := Files(root)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if got, want := names(strict), []string{"a.craftgo", "b.cg"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
	if got := names(FilesBestEffort(root)); !reflect.DeepEqual(got, names(strict)) {
		t.Errorf("FilesBestEffort = %v, want the same as Files (%v)", got, names(strict))
	}
}

// Load reads through the strict policy, so a half-readable design does
// not reach the analyser as a whole one.
func TestLoadRefusesAHalfReadableDesign(t *testing.T) {
	root := tree(t, map[string]string{
		"aaa/a.craftgo": "package aaa\n",
		"bbb/b.craftgo": "package bbb\n",
	})
	unreadable(t, filepath.Join(root, "bbb"))

	if srcs, err := Load(root); err == nil {
		t.Errorf("Load = %d sources, want a refusal", len(srcs))
	}
}

// A nil manifest is a design with no project above it. The options then
// carry a nil SecuritySchemes, which is what tells the reference check to
// stay quiet rather than calling every scheme undeclared.
func TestForWithNoManifestLeavesTheSchemeCheckDisabled(t *testing.T) {
	opts := For("/d", nil)
	if opts.SecuritySchemes != nil {
		t.Errorf("SecuritySchemes = %v, want nil - the check is disabled by nil, not by empty", opts.SecuritySchemes)
	}
	if opts.BasePath != "" || opts.FileCase != "" {
		t.Errorf("options = %+v, want the zero manifest values", opts)
	}
	if opts.DesignRoot != "/d" {
		t.Errorf("DesignRoot = %q, want the root even with no manifest", opts.DesignRoot)
	}
}

// An empty scheme map is also nil, for the same reason: a manifest that
// declares none gives no authoritative list to check against.
func TestForWithNoDeclaredSchemesLeavesTheCheckDisabled(t *testing.T) {
	if got := For("/d", &config.Config{}).SecuritySchemes; got != nil {
		t.Errorf("SecuritySchemes = %v, want nil", got)
	}
}

// The scheme list is rendered into the diagnostic's "known: ..." text, so
// a map walk would shuffle the message between runs.
func TestForSortsTheSchemeNames(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAPI.SecuritySchemes = map[string]config.SecurityScheme{
		"zeta": {}, "alpha": {}, "mu": {},
	}
	want := []string{"alpha", "mu", "zeta"}
	for i := 0; i < 8; i++ {
		if got := For("/d", cfg).SecuritySchemes; !reflect.DeepEqual(got, want) {
			t.Fatalf("SecuritySchemes = %v, want %v", got, want)
		}
	}
}

// The manifest's analysis inputs reach the options - these are the three
// the editor used to drop.
func TestForCarriesTheManifestsAnalysisInputs(t *testing.T) {
	cfg := &config.Config{}
	cfg.OpenAPI.BasePath = "/api"
	cfg.Output.FileCase = "kebab"
	cfg.OpenAPI.SecuritySchemes = map[string]config.SecurityScheme{"bearerAuth": {}}

	opts := For("/design", cfg)
	if opts.BasePath != "/api" || opts.FileCase != "kebab" || opts.DesignRoot != "/design" {
		t.Errorf("options = %+v", opts)
	}
	if got, want := opts.SecuritySchemes, []string{"bearerAuth"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SecuritySchemes = %v, want %v", got, want)
	}
}

// A file that declares no package is left alone: which package it belongs
// to is the analyser's rule, and naming one here would pre-empt it.
func TestParseLeavesAPackagelessFileAlone(t *testing.T) {
	parsed, diags := Parse([]Source{
		{Path: "/d/orders.craftgo", Text: "package orders\ntype O { id string }\n"},
		{Path: "/d/extra.craftgo", Text: "type E { id string }\n"},
	})
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	if len(parsed) != 2 {
		t.Fatalf("parsed %d files, want 2", len(parsed))
	}
	if parsed[0].File.Package == nil || parsed[0].File.Package.Name != "orders" {
		t.Errorf("first file's package = %+v", parsed[0].File.Package)
	}
	if parsed[1].File.Package != nil {
		t.Errorf("second file was given package %+v - that is the analyser's decision", parsed[1].File.Package)
	}
}

// The tokens ride along with the AST so no caller has to parse a second
// time to get them - the editor reads them on every keystroke.
func TestParseReturnsTokensBesideTheAST(t *testing.T) {
	parsed, _ := Parse([]Source{{Path: "/d/a.craftgo", Text: "package a\ntype T { id string }\n"}})
	if len(parsed) != 1 {
		t.Fatalf("parsed %d files, want 1", len(parsed))
	}
	if len(parsed[0].Tokens) == 0 {
		t.Error("no tokens returned, so a caller would have to parse again")
	}
	if got := ASTs(parsed); len(got) != 1 || got[0] != parsed[0].File {
		t.Errorf("ASTs did not return the parsed files")
	}
}

// Analyze is the whole path: parse, then analyse under the manifest's
// options, with both sets of diagnostics in that order.
func TestAnalyzeAppliesTheManifestOptions(t *testing.T) {
	const design = `package svc
type R {}
service S {
    get Health /healthz { response R }
}
`
	srcs := []Source{{Path: "/d/svc.craftgo", Text: design}}

	// With no basePath the route is /healthz and collides.
	proj, _, diags := Analyze(srcs, "/d", nil)
	if proj == nil {
		t.Fatal("no project")
	}
	if !hasCode(diags, "path/health-conflict") {
		t.Error("with no basePath /healthz collides and must be reported")
	}

	// With one it resolves to /api/healthz and does not.
	cfg := &config.Config{}
	cfg.OpenAPI.BasePath = "/api"
	if _, _, diags := Analyze(srcs, "/d", cfg); hasCode(diags, "path/health-conflict") {
		t.Error("basePath /api moves the route off the reserved path")
	}
}

func hasCode(diags []lexer.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
