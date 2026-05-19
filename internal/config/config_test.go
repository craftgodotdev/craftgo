package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a tiny test helper that writes content to path with default
// permissions and fails the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadDefaults pins the empty-manifest behaviour: with no keys set
// every Output.* path falls back to its framework default. Package is
// no longer a manifest field - it is resolved from go.mod at gen time
// - so an empty manifest is now a valid input.
func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.Types != "./internal/types" {
		t.Errorf("default types: %s", cfg.Output.Types)
	}
	if cfg.Output.Main != "./main.go" {
		t.Error("default main")
	}
	if cfg.Output.Config != "./config" {
		t.Error("default config")
	}
}

func TestLoadFullOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `output:
  types: ./gen/types
  main: ./cmd/api/main.go
  svccontext: ./internal/svc/svccontext.go
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.Types != "./gen/types" {
		t.Error()
	}
	if cfg.Output.Main != "./cmd/api/main.go" {
		t.Error()
	}
	if cfg.Output.Svccontext != "./internal/svc/svccontext.go" {
		t.Error()
	}
}

// TestLoadIgnoresStrayPackageKey: a `package:` key in the manifest
// is silently dropped (no struct field consumes it) so existing
// projects load cleanly. The truth-source for module path is
// exclusively go.mod.
func TestLoadIgnoresStrayPackageKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "package: github.com/old/manifest\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("legacy manifest with stray package: should still load: %v", err)
	}
	if cfg.Package != "" {
		t.Errorf("Package must be empty after Load (set later by ResolveModulePath); got %q", cfg.Package)
	}
}

func TestLoadBadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "not: valid: yaml: at: all\n  bad")
	if _, err := Load(path); err == nil {
		t.Error("expected parse error")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/no/such/path/craftgo.design.yaml"); err == nil {
		t.Error("expected error")
	}
}

// TestFindManifestInsideDesignFolder is the canonical layout: the manifest
// lives inside `design/`, and Find walks up from a sibling source dir to
// locate it.
func TestFindManifestInsideDesignFolder(t *testing.T) {
	root := t.TempDir()
	designDir := filepath.Join(root, "design")
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(designDir, Filename), "")
	// Walk from a deep sibling - Find should still discover design/ via
	// the project root.
	deep := filepath.Join(root, "internal", "logic", "userservice")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, projectRoot, foundDesign, err := Find(deep)
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if projectRoot != rootAbs {
		t.Errorf("project root: got %q want %q", projectRoot, rootAbs)
	}
	designAbs, _ := filepath.Abs(designDir)
	if foundDesign != designAbs {
		t.Errorf("design dir: got %q want %q", foundDesign, designAbs)
	}
}

// TestFindManifestAtRoot keeps the legacy layout working: manifest at the
// project root, design folder anywhere by convention. Project root is
// then the manifest's parent.
func TestFindManifestAtRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), "")
	_, projectRoot, designDir, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if designDir != rootAbs {
		t.Errorf("design dir: got %q want %q", designDir, rootAbs)
	}
	if projectRoot != filepath.Dir(rootAbs) {
		t.Errorf("project root: got %q want %q", projectRoot, filepath.Dir(rootAbs))
	}
}

func TestFindNotFound(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := Find(dir); err == nil {
		t.Error("expected error when manifest is missing")
	}
}

func TestFindBadManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, Filename), "not: valid: yaml: at: all\n  bad")
	if _, _, _, err := Find(dir); err == nil {
		t.Error("expected parse error")
	}
}

func TestFindBadManifestInsideDesign(t *testing.T) {
	root := t.TempDir()
	designDir := filepath.Join(root, "design")
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(designDir, Filename), "not: valid: yaml: at: all\n  bad")
	if _, _, _, err := Find(root); err == nil {
		t.Error("expected parse error")
	}
}

// TestResolveModulePathAtRoot pins the simple case: go.mod sits at
// projectRoot itself; the resolved path equals the module line verbatim.
func TestResolveModulePathAtRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/foo/bar\n\ngo 1.24\n")
	got, err := ResolveModulePath(root)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/bar" {
		t.Errorf("got %q, want %q", got, "github.com/foo/bar")
	}
}

// TestResolveModulePathMonorepo pins the shared-go.mod case: a single
// go.mod at the repo root, project root inside a sub-tree. The resolved
// path appends the relative path so generated imports compile.
func TestResolveModulePathMonorepo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/foo/monorepo\n")
	projectRoot := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveModulePath(projectRoot)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/monorepo/services/api" {
		t.Errorf("got %q, want %q", got, "github.com/foo/monorepo/services/api")
	}
}

// TestResolveModulePathNoGoMod pins the missing-go.mod error path: the
// returned message MUST mention `go mod init` so users get a concrete
// fix to copy-paste.
func TestResolveModulePathNoGoMod(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveModulePath(deep)
	if err == nil {
		t.Fatal("expected error when no go.mod is found")
	}
}

// TestResolveModulePathClosestGoMod pins the multi-go.mod precedence:
// a sub-module's go.mod takes precedence over a parent's, matching
// Go's own resolution rules.
func TestResolveModulePathClosestGoMod(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/foo/parent\n")
	subModule := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(subModule, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subModule, "go.mod"), "module github.com/foo/api\n")
	got, err := ResolveModulePath(subModule)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/api" {
		t.Errorf("got %q, want %q (closest go.mod wins)", got, "github.com/foo/api")
	}
}

// TestResolveModulePathQuotedModuleLine pins the rare but legal form
// `module "github.com/foo/bar"` - go.mod accepts quoted paths.
func TestResolveModulePathQuotedModuleLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module \"github.com/foo/bar\"\n")
	got, err := ResolveModulePath(root)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/bar" {
		t.Errorf("got %q, want %q", got, "github.com/foo/bar")
	}
}
