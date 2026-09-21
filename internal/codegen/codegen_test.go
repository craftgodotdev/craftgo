package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
	"github.com/craftgodotdev/craftgo/internal/config"
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
			FileCase:   config.FileCaseSnake,
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

// A project with no event generates nothing, so adding the feature costs
// existing projects no output.
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

// A disabled target is skipped without disabling the rest.
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

// The catalogue and the set of languages the manifest accepts must match
// exactly. A language the manifest accepts with no row generates nothing;
// a row for a language the manifest rejects can never run.
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

// Every target writes into its own configured directory, so one run can
// feed a Go service and the documents that describe it.
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

// A narrowed run must not touch another target's output. Every target
// prunes what it owns, so running one while another is selected out would
// otherwise delete the unselected target's files.
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
	// Regenerate only the documents; the Go output must survive.
	if err := Generate(Inputs{Design: proj}, cfg, dir, TargetDocs); err != nil {
		t.Fatalf("generate docs: %v", err)
	}
	if _, err := os.Stat(goFile); err != nil {
		t.Errorf("a docs-only run deleted the Go output: %v", err)
	}
}

// An unknown target name fails rather than silently generating less.
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

// Every selectable name must be one the run actually understands.
func TestSelectableTargetsAreKnown(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	for _, name := range SelectableTargets() {
		if err := Generate(Inputs{Design: proj}, eventsConfig(), t.TempDir(), name); err != nil {
			t.Errorf("target %q is offered but does not run: %v", name, err)
		}
	}
}

// generatedHeader opens every file craftgo rewrites on each run; the
// scaffolds carry a different first line.
const generatedHeader = "// Code generated by craftgo. DO NOT EDIT."

const alphaSrc = `package x
type Placed { id string @minLength(1) }
event Placed { payload Placed }`

// notesMatching picks the notes a test means out of the whole set, so
// adding an unrelated note does not fail it.
func notesMatching(notes []string, needle string) []string {
	var out []string
	for _, n := range notes {
		if strings.Contains(n, needle) {
			out = append(out, n)
		}
	}
	return out
}

// A contracts project writes none of the application half, so it sweeps
// none of those directories either. The leftovers of a project that used
// to be an application ship with the library unless someone deletes them,
// so they are named.
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

// `output.main: "-"` skips the ServiceContext scaffold while the handlers,
// logic stubs and wiring are all written against it. A project that has
// not written its own is told so.
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
