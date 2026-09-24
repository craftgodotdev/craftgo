package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

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
			Targets: []config.EventTarget{{Lang: config.LangGo, Out: "./internal/events"}},
		},
	}
}

func analyzeProject(t *testing.T, sources ...string) *semantic.Project {
	t.Helper()
	files := make([]*ast.File, 0, len(sources))
	for i, src := range sources {
		p := parser.New("test.craftgo", src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse errors in source %d: %v", i, d)
		}
		files = append(files, f)
	}
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{})
	for _, d := range diags {
		if d.Severity == lexer.SeverityError {
			t.Fatalf("semantic errors: %v", diags)
		}
	}
	return proj
}

const ordersSrc = `package orders
type OrderPlacedPayload { orderId string }
event OrderPlaced { payload OrderPlacedPayload }`

// A design with no event makes the event targets write nothing.
func TestNoEventsGeneratesNothing(t *testing.T) {
	proj := analyzeProject(t, `package p
type P { id string }
service S {
	get Read /r { response P }
}`)
	dir := t.TempDir()
	if err := GenerateEventTargets(proj, eventsConfig(), dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no output, got %v", entries)
	}
}

// A target whose out is "-" writes nothing.
func TestDisabledTargetIsSkipped(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	cfg := eventsConfig()
	cfg.Events.Targets = []config.EventTarget{{Lang: config.LangGo, Out: "-"}}
	dir := t.TempDir()
	if err := GenerateEventTargets(proj, cfg, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "events")); !os.IsNotExist(err) {
		t.Errorf("disabled Go target still produced output (%v)", err)
	}
}

// LangTargets has exactly one row per language the manifest accepts.
func TestCatalogueMatchesSupportedLangs(t *testing.T) {
	inCatalogue := map[string]bool{}
	for _, target := range LangTargets {
		if inCatalogue[target.Lang] {
			t.Errorf("%q has two rows in the catalogue", target.Lang)
		}
		inCatalogue[target.Lang] = true
	}
	for _, lang := range config.SupportedLangs {
		if !inCatalogue[lang] {
			t.Errorf("config accepts %q but no target generates it", lang)
		}
		delete(inCatalogue, lang)
	}
	for lang := range inCatalogue {
		t.Errorf("catalogue generates %q but config rejects it", lang)
	}
}

// One pass writes the Go event library and the OpenAPI document, each where
// it is configured.
func TestEveryEnabledTargetWritesItsOwnOutput(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	dir := t.TempDir()
	if err := Generate(Inputs{Design: proj}, eventsConfig(), dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{
		filepath.Join("internal", "events", "orders", "events.go"),
		filepath.Join("docs", "openapi.yaml"),
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("target output missing: %s (%v)", want, err)
		}
	}
}

// A run narrowed to one target leaves the other targets' output in place.
func TestTargetSelectionLeavesOtherOutputAlone(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	cfg := eventsConfig()
	dir := t.TempDir()
	if err := Generate(Inputs{Design: proj}, cfg, dir); err != nil {
		t.Fatalf("generate all: %v", err)
	}
	goFile := filepath.Join(dir, "internal", "events", "orders", "events.go")
	docFile := filepath.Join(dir, "docs", "openapi.yaml")
	for _, f := range []string{goFile, docFile} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("first pass missing %s: %v", f, err)
		}
	}
	// Regenerate only the document; the Go output survives.
	if err := Generate(Inputs{Design: proj}, cfg, dir, TargetDocs); err != nil {
		t.Fatalf("generate docs: %v", err)
	}
	if _, err := os.Stat(goFile); err != nil {
		t.Errorf("a docs-only run deleted the Go output: %v", err)
	}
}

// An unknown target name fails the run with the list of valid ones.
func TestUnknownTargetIsRejected(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	err := Generate(Inputs{Design: proj}, eventsConfig(), t.TempDir(), "rust")
	if err == nil {
		t.Fatal("expected an error for an unknown target")
	}
	for _, want := range []string{"rust", "go", "docs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Every name SelectableTargets offers runs.
func TestSelectableTargetsAreKnown(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	for _, name := range SelectableTargets() {
		if err := Generate(Inputs{Design: proj}, eventsConfig(), t.TempDir(), name); err != nil {
			t.Errorf("target %q is offered but does not run: %v", name, err)
		}
	}
}

// generatedHeader opens every Go file craftgo rewrites on each run.
const generatedHeader = "// Code generated by craftgo. DO NOT EDIT."

const alphaSrc = `package x
type Placed { id string @minLength(1) }
event Placed { payload Placed }`

// notesMatching returns the notes that contain needle.
func notesMatching(notes []string, needle string) []string {
	var out []string
	for _, n := range notes {
		if strings.Contains(n, needle) {
			out = append(out, n)
		}
	}
	return out
}

// A contracts project names leftover application output and does not
// delete it.
func TestContractsProjectNamesLeftoverApplicationOutput(t *testing.T) {
	root := t.TempDir()
	cfg := eventsConfig()
	cfg.Output.Kind = config.KindContracts
	cfg.Output.Main = "./main.go"

	stale := filepath.Join(root, filepath.FromSlash(cfg.Output.Transport), "svc", "handler.go")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(generatedHeader+"\n\npackage svc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	notes := notesMatching(golang.EventOutputNotes(analyzeProject(t, alphaSrc), nil, cfg, root), "output.kind is contracts")
	if len(notes) != 1 || !strings.Contains(notes[0], cfg.Output.Transport) {
		t.Errorf("leftover application output must be named: %v", notes)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("it is named, not deleted: %v", err)
	}
}

// With `output.main: "-"` a note asks for the ServiceContext until the
// project writes one.
func TestRuntimeDisabledNamesTheMissingContainer(t *testing.T) {
	root := t.TempDir()
	cfg := eventsConfig()
	cfg.Output.Main = "-"
	proj := analyzeProject(t, alphaSrc)

	if notes := notesMatching(golang.EventOutputNotes(proj, nil, cfg, root), "yours to write"); len(notes) != 1 {
		t.Errorf("a project with no container must be told: %v", golang.EventOutputNotes(proj, nil, cfg, root))
	}

	// Once the project supplies one, the note goes.
	dest := filepath.Join(root, filepath.FromSlash(cfg.Output.Svccontext))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("package svccontext\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if notes := notesMatching(golang.EventOutputNotes(proj, nil, cfg, root), "yours to write"); len(notes) != 0 {
		t.Errorf("a hand-written container must silence it: %v", notes)
	}
}
