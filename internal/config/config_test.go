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

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `package: github.com/x/y
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Package != "github.com/x/y" {
		t.Error("package")
	}
	// Defaults applied
	if cfg.Output.Types != "./internal/types" {
		t.Errorf("default types: %s", cfg.Output.Types)
	}
	if cfg.Output.Main != "./main.go" {
		t.Error("default main")
	}
	if cfg.Templates.Dir != "./.craftgo/templates" {
		t.Error("default templates dir")
	}
}

func TestLoadFullOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `package: github.com/x/y
output:
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

func TestLoadMissingPackage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "\n")
	if _, err := Load(path); err == nil {
		t.Error("expected error for missing package")
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
	writeFile(t, filepath.Join(designDir, Filename), `package: github.com/x/y
`)
	// Walk from a deep sibling — Find should still discover design/ via
	// the project root.
	deep := filepath.Join(root, "internal", "logic", "userservice")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, projectRoot, foundDesign, err := Find(deep)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Package != "github.com/x/y" {
		t.Error("package")
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
	writeFile(t, filepath.Join(root, Filename), `package: github.com/x/y
`)
	cfg, projectRoot, designDir, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Package != "github.com/x/y" {
		t.Error("package")
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
	writeFile(t, filepath.Join(dir, Filename), "::: not yaml")
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
	writeFile(t, filepath.Join(designDir, Filename), "::: not yaml")
	if _, _, _, err := Find(root); err == nil {
		t.Error("expected parse error")
	}
}
