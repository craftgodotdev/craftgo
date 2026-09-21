package docs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// docsConfig points the document at docs/openapi.yaml, the default.
func docsConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Output.OpenAPI = "./docs/openapi.yaml"
	return cfg
}

// A design of protos alone has no DSL package, so the document would
// carry an empty `paths` and an empty `components` and nothing would
// serve it. None is written, and the directory is still swept so one an
// earlier run left behind goes with the package that seeded it.
func TestNoDocumentWithoutADSLPackage(t *testing.T) {
	dir := t.TempDir()
	cfg := docsConfig()
	empty, _ := semantic.AnalyzeProject(nil, semantic.Options{})

	if err := GenerateOpenAPI(empty, cfg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "openapi.yaml")); !os.IsNotExist(err) {
		t.Errorf("a project with no DSL package got a document: %v", err)
	}
	if got := RegeneratedFile(empty, cfg, dir); got != "" {
		t.Errorf("plan names %q, but nothing writes it", got)
	}
	if got := OutputDir(cfg, dir); got != filepath.Join(dir, "docs", "openapi.yaml") {
		t.Errorf("the sweep must still reach the directory, got %q", got)
	}
	if got := OutputDir(&config.Config{}, dir); got != "" {
		t.Errorf("a disabled document has no directory, got %q", got)
	}
}
