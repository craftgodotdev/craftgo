package designopts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// tree writes files, keyed by relative path, under a fresh root and returns it.
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

// unreadable makes dir unreadable, skipping the test where it cannot (as root).
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

// names returns the base names of paths.
func names(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// TestFilesBestEffortSkipsAnUnreadableDirectoryAndKeepsGoing checks that
// c.craftgo, walked after the unreadable bbb/, is still found.
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

// TestLoadRefusesAHalfReadableDesign checks that Load fails like Files on an
// unreadable directory.
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

// TestForWithNoManifestLeavesTheSchemeCheckDisabled checks that a nil manifest
// yields nil SecuritySchemes and zero manifest options.
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

// TestForWithNoDeclaredSchemesLeavesTheCheckDisabled checks that an empty
// scheme map also yields nil SecuritySchemes.
func TestForWithNoDeclaredSchemesLeavesTheCheckDisabled(t *testing.T) {
	if got := For("/d", &config.Config{}).SecuritySchemes; got != nil {
		t.Errorf("SecuritySchemes = %v, want nil", got)
	}
}

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

// TestForCarriesTheManifestsAnalysisInputs checks that basePath, fileCase and
// the scheme names reach the options.
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

// TestAnalyzeAppliesTheManifestOptions checks that Analyze honours the
// manifest's basePath.
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

// ProjectOf finds the project whose design root holds a file, and none for a
// file beside a design folder or outside any.
func TestProjectOf(t *testing.T) {
	root := tree(t, map[string]string{
		"design/craftgo.design.yaml": "openapi:\n  title: Probe\n",
		"design/app/app.craftgo":     "package app\n",
		"stray.craftgo":              "package stray\n",
	})
	design := filepath.Join(root, "design")
	cfg, got := ProjectOf(filepath.Join(design, "app", "app.craftgo"))
	if got != design || cfg == nil || cfg.OpenAPI.Title != "Probe" {
		t.Errorf("file under the design root: root %q, config %+v", got, cfg)
	}
	for _, path := range []string{filepath.Join(root, "stray.craftgo"), filepath.Join(t.TempDir(), "x.craftgo"), ""} {
		if cfg, got := ProjectOf(path); got != "" || cfg != nil {
			t.Errorf("ProjectOf(%q) = %+v, %q, want no project", path, cfg, got)
		}
	}
}

// FileErrors keeps the errors in the file and those tied to no file.
func TestFileErrors(t *testing.T) {
	at := func(file string, sev lexer.Severity, msg string) lexer.Diagnostic {
		return lexer.Diagnostic{Pos: lexer.Position{Filename: file, Line: 1}, Severity: sev, Msg: msg}
	}
	diags := []lexer.Diagnostic{
		at("a.craftgo", lexer.SeverityError, "in a"),
		at("b.craftgo", lexer.SeverityError, "in b"),
		at("a.craftgo", lexer.SeverityWarning, "warning in a"),
		at("", lexer.SeverityError, "in no file"),
	}
	var got []string
	for _, d := range FileErrors(diags, "a.craftgo") {
		got = append(got, d.Msg)
	}
	if want := []string{"in a", "in no file"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FileErrors = %q, want %q", got, want)
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

func TestProtoOptionsFollowTheManifest(t *testing.T) {
	cfg := &config.Config{Package: "example.com/app"}
	cfg.Output.PB = "./internal/pb"
	cfg.Output.FileCase = "kebab"
	cfg.Proto.Includes = []string{"./third_party"}
	got := ProtoOptions(cfg, filepath.Join("/", "proj"))
	if got.Module != "example.com/app" || got.PBDir != "./internal/pb" || got.FileCase != "kebab" {
		t.Errorf("options = %+v", got)
	}
	if want := filepath.Join("/", "proj", "third_party"); len(got.Includes) != 1 || got.Includes[0] != want {
		t.Errorf("includes = %v", got.Includes)
	}
	cfg.Output.PB = "-"
	if got := ProtoOptions(cfg, "/proj"); got.PBDir != "" {
		t.Errorf(`pb "-" must leave PBDir empty, got %q`, got.PBDir)
	}
}
