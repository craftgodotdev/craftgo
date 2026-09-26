package golang

import (
	"context"
	"flag"
	goast "go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	gopath "path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func analyze(t *testing.T, src string) *semantic.Package {
	t.Helper()
	p := craftparser.New("test.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse errors: %v", d)
	}
	pkg, diags := semantic.Analyze([]*ast.File{f})
	// A warning still generates valid code, so only errors fail the test.
	var fatal []semantic.Diagnostic
	for _, d := range diags {
		if d.Severity == lexer.SeverityError {
			fatal = append(fatal, d)
		}
	}
	if len(fatal) > 0 {
		t.Fatalf("semantic errors: %v", fatal)
	}
	return pkg
}

// analyzeProject analyses sources as one project and fails on any error diagnostic.
func analyzeProject(t *testing.T, sources ...string) *semantic.Project {
	t.Helper()
	files := make([]*ast.File, 0, len(sources))
	for i, src := range sources {
		p := craftparser.New("test.craftgo", src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse errors in source %d: %v", i, d)
		}
		files = append(files, f)
	}
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{})
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("semantic errors: %v", diags)
		}
	}
	return proj
}

// projectFiles writes src under a temp root and returns the root and the parsed files.
func projectFiles(t *testing.T, src map[string]string) (string, []*ast.File) {
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
		p := craftparser.New(full, content)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse %s: %v", rel, d)
		}
		files = append(files, f)
	}
	return root, files
}

func sampleConfig() *config.Config {
	return &config.Config{
		Package: "github.com/example/app",
		Output: config.Output{
			Types:      "./internal/types",
			Transport:  "./internal/transport",
			Routes:     "./internal/routes",
			Service:    "./internal/service",
			Svccontext: "./svccontext/svccontext.go",
			Wiring:     "./internal/wiring",
			OpenAPI:    "./docs/openapi.yaml",
			FileCase:   idents.FileCaseKebab,
		},
		OpenAPI: config.OpenAPI{BasePath: "/v1"},
	}
}

func newFixtureConfig() *config.Config {
	return &config.Config{
		Package: "github.com/test/m",
		Output:  config.Output{Types: "./internal/types"},
	}
}

// goEventsOut is the Go target's destination in [eventsConfig].
const goEventsOut = "./internal/events"

// eventsConfig is a manifest with the Go event target enabled.
func eventsConfig() *config.Config {
	return &config.Config{
		Package: "example.com/app",
		Output: config.Output{
			Types:      "./internal/types",
			Transport:  "./internal/transport",
			Routes:     "./internal/routes",
			Service:    "./internal/service",
			Svccontext: "./svccontext/svccontext.go",
			Wiring:     "./internal/wiring",
			Middleware: "./internal/middleware",
			Config:     "./config",
			OpenAPI:    "./docs/openapi.yaml",
			Main:       "-",
			FileCase:   idents.FileCaseSnake,
		},
		OpenAPI: config.OpenAPI{Title: "Events", Version: "1.0.0"},
		Events: config.Events{
			Targets: []config.EventTarget{{Lang: config.LangGo, Out: goEventsOut}},
		},
	}
}

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

// runValidateGen returns the validate.go generated for src.
func runValidateGen(t *testing.T, src string) string {
	t.Helper()
	pkg := analyze(t, src)
	dir := t.TempDir()
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "validate.go"))
	if err != nil {
		t.Fatal(err)
	}
	mustParseGo(t, string(out))
	return string(out)
}

// genRoutes writes pkg's routes files and the umbrella as a single-package project.
func genRoutes(t *testing.T, pkg *semantic.Package, cfg *config.Config, root string) error {
	t.Helper()
	if err := generateRoutes(pkg, cfg, root); err != nil {
		return err
	}
	proj := &semantic.Project{Packages: map[string]*semantic.Package{pkg.Name: pkg}}
	return generateProjectRoutesUmbrella(proj, cfg, root)
}

func readGen(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// loadProtos compiles the testdata/proto fixture as `craftgo gen` would for cfg.
func loadProtos(t *testing.T, cfg *config.Config) *protodesign.Set {
	t.Helper()
	set, err := protodesign.Load(context.Background(), filepath.Join("testdata", "proto"), designopts.ProtoOptions(cfg, "."))
	if err != nil {
		t.Fatal(err)
	}
	if !set.HasServices() {
		t.Fatal("fixture declares no service")
	}
	return set
}

func greeter(t *testing.T, set *protodesign.Set) *protodesign.Service {
	t.Helper()
	for _, svc := range set.Services {
		if svc.Name == "Greeter" {
			return svc
		}
	}
	t.Fatal("no Greeter")
	return nil
}

func mustParseGo(t *testing.T, src string) {
	t.Helper()
	file, err := parser.ParseFile(gotoken.NewFileSet(), "out.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated Go does not parse: %v\n--- source ---\n%s", err, src)
	}
	mustImportsMatchUsage(t, file, src)
}

// stdlibQualifiers maps the standard-library qualifiers the import check covers to their paths.
var stdlibQualifiers = map[string]string{
	"fmt":       "fmt",
	"errors":    "errors",
	"strconv":   "strconv",
	"strings":   "strings",
	"time":      "time",
	"regexp":    "regexp",
	"utf8":      "unicode/utf8",
	"reflect":   "reflect",
	"json":      "encoding/json",
	"base64":    "encoding/base64",
	"http":      "net/http",
	"url":       "net/url",
	"io":        "io",
	"os":        "os",
	"sort":      "sort",
	"sync":      "sync",
	"context":   "context",
	"multipart": "mime/multipart",
	"mail":      "net/mail",
	"netip":     "net/netip",
	"slices":    "slices",
	"maps":      "maps",
}

// mustImportsMatchUsage asserts file imports exactly the stdlibQualifiers packages it uses, binds
// each import name once and uses every package it imports under an alias.
func mustImportsMatchUsage(t *testing.T, file *goast.File, src string) {
	t.Helper()

	imported := map[string]bool{}
	aliased := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := gopath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
			aliased[name] = true
		}
		if imported[name] {
			t.Errorf("generated Go imports two packages as %s\n--- source ---\n%s", name, src)
		}
		imported[name] = true
	}

	used := map[string]bool{}
	goast.Inspect(file, func(n goast.Node) bool {
		sel, ok := n.(*goast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*goast.Ident); ok {
			used[ident.Name] = true
		}
		return true
	})

	for name := range used {
		path, std := stdlibQualifiers[name]
		if std && !imported[name] {
			t.Errorf("generated Go uses %s.* but does not import %q\n--- source ---\n%s", name, path, src)
		}
	}
	for name := range imported {
		if name == "_" || name == "." {
			continue
		}
		if _, std := stdlibQualifiers[name]; (std || aliased[name]) && !used[name] {
			t.Errorf("generated Go imports %q but never uses it\n--- source ---\n%s", name, src)
		}
	}
}

// mustContainAll asserts every want substring appears in got, reporting all misses at once.
func mustContainAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	var missing []string
	for _, w := range wants {
		if !strings.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Errorf("output missing %d expected substring(s):\n  - %s\n--- got ---\n%s",
			len(missing), strings.Join(missing, "\n  - "), got)
	}
}

// mustContainNone asserts no unwanted substring appears in got.
func mustContainNone(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	var present []string
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			present = append(present, w)
		}
	}
	if len(present) > 0 {
		t.Errorf("output unexpectedly contains %d forbidden substring(s):\n  - %s\n--- got ---\n%s",
			len(present), strings.Join(present, "\n  - "), got)
	}
}

// updateGolden (-update) makes expectGolden rewrite the testdata/golden files.
var updateGolden = flag.Bool("update", false, "rewrite golden snapshot files instead of comparing")

// expectGolden compares actual with testdata/golden/<name>, or rewrites that file under -update.
func expectGolden(t *testing.T, name, actual string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata/golden: %v", err)
		}
		if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("wrote golden %s (%d bytes)", path, len(actual))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s does not exist; run with -update to create it", path)
		}
		t.Fatalf("read golden %s: %v", path, err)
	}
	// A Windows checkout may give the golden file CRLF line endings; generated output is LF.
	wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")
	if wantStr == actual {
		return
	}
	t.Errorf("golden mismatch (%s) - diff first divergence:\n%s", path, firstDiff(wantStr, actual))
}

// firstDiff returns the want and got lines around their first difference.
func firstDiff(want, got string) string {
	wantLines := strings.SplitSeq(want, "\n")
	gotLines := strings.SplitSeq(got, "\n")
	wIter, gIter := wantLines, gotLines
	wantSlice := stringsCollect(wIter)
	gotSlice := stringsCollect(gIter)
	max := len(wantSlice)
	if len(gotSlice) > max {
		max = len(gotSlice)
	}
	for i := 0; i < max; i++ {
		w, g := "", ""
		if i < len(wantSlice) {
			w = wantSlice[i]
		}
		if i < len(gotSlice) {
			g = gotSlice[i]
		}
		if w != g {
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 4
			if end > max {
				end = max
			}
			var sb strings.Builder
			for j := start; j < end; j++ {
				marker := "  "
				if j == i {
					marker = "→ "
				}
				wj, gj := "", ""
				if j < len(wantSlice) {
					wj = wantSlice[j]
				}
				if j < len(gotSlice) {
					gj = gotSlice[j]
				}
				sb.WriteString(marker)
				sb.WriteString("want: ")
				sb.WriteString(wj)
				sb.WriteString("\n")
				sb.WriteString(marker)
				sb.WriteString("got:  ")
				sb.WriteString(gj)
				sb.WriteString("\n")
			}
			return sb.String()
		}
	}
	return "(strings differ in length but match line-by-line up to the shorter end)"
}

// stringsCollect drains a strings.SplitSeq iterator into a slice.
func stringsCollect(it func(yield func(string) bool)) []string {
	var out []string
	it(func(s string) bool {
		out = append(out, s)
		return true
	})
	return out
}
