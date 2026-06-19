// Package e2e hosts the cross-fixture orchestrator. Every immediate
// subdirectory of `tests/e2e/` that contains a go.mod is treated as
// an independent scenario: the orchestrator runs `craftgo gen`
// against it and then `go test ./...` inside it. Adding a new
// scenario is a matter of dropping a self-contained Go module into
// this directory.
//
// The fixtures live under `tests/e2e/` (not `testdata/`) on
// purpose - they are full Go modules with their own tests, not
// read-only fixture data. Keeping them next to the orchestrator
// makes the layout self-explanatory.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the absolute path of the craftgo module root -
// three levels up from this file (tests/e2e/orchestrator_test.go).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(here)))
}

// scenariosDir returns the absolute path of tests/e2e/, the directory
// holding every isolated fixture module.
func scenariosDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Dir(here)
}

// discoverScenarios returns every immediate child of tests/e2e/ that
// contains a go.mod. Adding a new fixture is just `mkdir + go mod
// init` - the orchestrator picks it up on the next run.
func discoverScenarios(t *testing.T) []string {
	t.Helper()
	root := scenariosDir(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "go.mod")); err == nil {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		t.Fatalf("no scenarios with go.mod found under %s", root)
	}
	return out
}

// TestE2EFullPipeline runs `craftgo gen` against every scenario and then
// invokes `go test ./...` inside it. Each scenario is an isolated Go
// module so a regression in one does not mask failures in another.
func TestE2EFullPipeline(t *testing.T) {
	repo := repoRoot(t)
	root := scenariosDir(t)
	scenarios := discoverScenarios(t)
	for _, name := range scenarios {
		name := name
		t.Run(name, func(t *testing.T) {
			fixture := filepath.Join(root, name)

			gen := exec.Command("go", "run", "./cmd/craftgo", "gen", fixture)
			gen.Dir = repo
			if out, err := gen.CombinedOutput(); err != nil {
				t.Fatalf("craftgo gen failed: %v\n%s", err, out)
			}

			test := exec.Command("go", "test", "./...")
			test.Dir = fixture
			out, err := test.CombinedOutput()
			if err != nil {
				t.Fatalf("scenario %q tests failed: %v\n%s", name, err, out)
			}
			if !strings.Contains(string(out), "ok") {
				t.Errorf("scenario %q produced no `ok` line:\n%s", name, out)
			}
		})
	}
}
