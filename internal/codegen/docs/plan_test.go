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

// A design without a DSL package gets no document, and the plan still names
// its directory for the sweep.
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
	dirs, files := Plan(empty, cfg, dir)
	if len(files) != 0 {
		t.Errorf("plan names %v, but nothing writes them", files)
	}
	if _, ok := dirs[filepath.Join(dir, "docs")]; !ok || len(dirs) != 1 {
		t.Errorf("the sweep must still reach the directory, got %v", dirs)
	}
	if dirs, files := Plan(empty, &config.Config{}, dir); dirs != nil || files != nil {
		t.Errorf("a disabled document has no directory, got %v and %v", dirs, files)
	}
}
