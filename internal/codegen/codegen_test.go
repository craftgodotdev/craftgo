package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
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
service OrderService {
	event OrderPlaced { payload OrderPlacedPayload }
}`

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
	if err := Generate(proj, eventsConfig(), dir); err != nil {
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
	if err := Generate(proj, cfg, dir); err != nil {
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
	if err := Generate(proj, cfg, dir, TargetDocs); err != nil {
		t.Fatalf("generate docs: %v", err)
	}
	if _, err := os.Stat(goFile); err != nil {
		t.Errorf("a docs-only run deleted the Go output: %v", err)
	}
}

// An unknown target name fails rather than silently generating less.
func TestUnknownTargetIsRejected(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	err := Generate(proj, eventsConfig(), t.TempDir(), "rust")
	if err == nil {
		t.Fatal("expected an error for an unknown target")
	}
	for _, want := range []string{"rust", "go", "docs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// `--target typescript` names a target craftgo used to generate, so it is
// told the target was removed rather than that the name is unknown.
func TestRemovedTargetIsNamedAsRemoved(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	err := Generate(proj, eventsConfig(), t.TempDir(), "typescript")
	if err == nil {
		t.Fatal("a removed target must be rejected")
	}
	for _, want := range []string{"typescript", "was removed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Every selectable name must be one the run actually understands.
func TestSelectableTargetsAreKnown(t *testing.T) {
	proj := analyzeProject(t, ordersSrc)
	for _, name := range SelectableTargets() {
		if err := Generate(proj, eventsConfig(), t.TempDir(), name); err != nil {
			t.Errorf("target %q is offered but does not run: %v", name, err)
		}
	}
}
