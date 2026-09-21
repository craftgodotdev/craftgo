package golang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// The gen-once scaffolds - main.go, the config package, svccontext.go -
// are written only when missing, so the committed examples never
// regenerate them and the drift guard cannot see a template change.
// These goldens pin the HTTP-only output instead: a mismatch is either
// an intended template edit, refreshed with -update, or a branch meant
// for another transport leaking into a project that has none.

// scaffoldConfig is a manifest with every default applied, the way
// `craftgo gen` sees an empty craftgo.design.yaml.
func scaffoldConfig(t *testing.T) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), config.Filename)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Package = "example.com/app"
	return cfg
}

// httpScaffoldSrc is a design with one route and one applied middleware,
// so the scaffolds render their middleware wiring and the wiring its
// guard.
const httpScaffoldSrc = `package todos
middleware AuthRequired
type Todo { id string }
service TodoService {
	@middlewares(AuthRequired)
	get ListTodos /todos { response Todo }
}`

func TestHTTPScaffoldsArePinned(t *testing.T) {
	cfg := scaffoldConfig(t)
	proj := analyzeProject(t, httpScaffoldSrc)

	mainGo, err := renderGo(tmpl("main.tmpl"), buildProjectMainData(proj, nil, cfg))
	if err != nil {
		t.Fatal(err)
	}
	expectGolden(t, "main-http.go", string(mainGo))

	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  goImportFromRel(cfg.Package, cfg.Output.Config),
	}
	for _, f := range []struct {
		template string
		formatGo bool
		golden   string
	}{
		{"config.go.tmpl", true, "config-http.go"},
		{"config.yaml.tmpl", false, "config-http.yaml"},
		{"example.config.yaml.tmpl", false, "example-config-http.yaml"},
		{"svccontext.go.tmpl", true, "svccontext-http.go"},
	} {
		body, err := renderRuntimeTemplate(f.template, data, f.formatGo)
		if err != nil {
			t.Fatalf("%s: %v", f.template, err)
		}
		expectGolden(t, f.golden, string(body))
	}

	dir := t.TempDir()
	if err := generateWiring(proj, cfg, dir); err != nil {
		t.Fatal(err)
	}
	wiring, err := os.ReadFile(filepath.Join(dir, cfg.Output.Wiring, "wiring.go"))
	if err != nil {
		t.Fatal(err)
	}
	expectGolden(t, "wiring-http.go", string(wiring))
}
