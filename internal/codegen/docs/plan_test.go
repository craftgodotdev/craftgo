package docs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// docsConfig puts the document at docs/openapi.yaml.
func docsConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Output.OpenAPI = "./docs/openapi.yaml"
	return cfg
}

// A design without a DSL package gets no document, and DocumentPath still
// names its path for the sweep.
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
	if got := DocumentPath(cfg, dir); got != filepath.Join(dir, "docs", "openapi.yaml") {
		t.Errorf("the sweep must still reach the directory, got %q", got)
	}
	if got := DocumentPath(&config.Config{}, dir); got != "" {
		t.Errorf("a disabled document has no directory, got %q", got)
	}
}
