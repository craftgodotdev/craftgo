package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/craftgodotdev/craftgo/internal/config"
)

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

// httpScaffoldSrc runs one route behind a middleware, so the scaffolds wire it and guard it.
const httpScaffoldSrc = `package todos
middleware AuthRequired
type Todo { id string }
service TodoService {
	@middlewares(AuthRequired)
	get ListTodos /todos { response Todo }
}`

// The scaffolds and wiring of an HTTP-only project match their goldens.
func TestHTTPScaffoldsArePinned(t *testing.T) {
	cfg := scaffoldConfig(t)
	proj := analyzeProject(t, httpScaffoldSrc)

	mainGo, err := renderScaffold(tmpl("main.tmpl"), buildProjectMainData(proj, nil, cfg))
	if err != nil {
		t.Fatal(err)
	}
	expectGolden(t, "main-http.go", string(mainGo))

	data := buildRuntimeData(proj, nil, cfg)
	for _, f := range []struct {
		template string
		render   func(*template.Template, any) ([]byte, error)
		golden   string
	}{
		{"config.go.tmpl", renderScaffold, "config-http.go"},
		{"config.yaml.tmpl", execute, "config-http.yaml"},
		{"example.config.yaml.tmpl", execute, "example-config-http.yaml"},
		{"svccontext.go.tmpl", renderScaffold, "svccontext-http.go"},
	} {
		body, err := f.render(tmpl(f.template), data)
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

// config.Path names, relative to the project root, the config.yaml gen writes under output.config,
// and the scaffold comments name that directory.
func TestConfigPathFollowsOutputConfig(t *testing.T) {
	for _, c := range []struct {
		dir, path string
	}{
		{"./internal/config", "internal/config/config.yaml"},
		{"./", "./config.yaml"},
	} {
		t.Run(c.dir, func(t *testing.T) {
			cfg := scaffoldConfig(t)
			cfg.Output.Config = c.dir
			root := t.TempDir()
			proj := analyzeProject(t, httpScaffoldSrc)
			if err := generateRuntimeConfig(proj, nil, cfg, root); err != nil {
				t.Fatal(err)
			}
			configGo, err := os.ReadFile(filepath.Join(root, c.dir, "config.go"))
			if err != nil {
				t.Fatal(err)
			}
			mustContainAll(t, string(configGo), `return "`+c.path+`"`, "`"+c.path+"`")
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.path))); err != nil {
				t.Errorf("config.yaml is not where Path points: %v", err)
			}
			mainGo, err := renderScaffold(tmpl("main.tmpl"), buildProjectMainData(proj, nil, cfg))
			if err != nil {
				t.Fatal(err)
			}
			mustContainAll(t, string(mainGo), strings.TrimSuffix(c.path, "config.yaml")+"example.config.yaml")
		})
	}
}
