// Package e2e treats each subdirectory of tests/e2e holding a go.mod as a
// scenario: it runs craftgo gen on the scenario, then go test ./... inside it.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// repoRoot returns the absolute path of the craftgo module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(here)))
}

// scenariosDir returns the absolute path of tests/e2e.
func scenariosDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	return filepath.Dir(here)
}

// discoverScenarios returns each child directory of tests/e2e holding a go.mod.
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

// discoverManifests returns the folder of every manifest under fixture.
func discoverManifests(t *testing.T, fixture string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(fixture, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == config.Filename {
			out = append(out, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no %s found under %s", config.Filename, fixture)
	}
	return out
}

// TestE2EFullPipeline regenerates each scenario in place, then runs its tests.
func TestE2EFullPipeline(t *testing.T) {
	repo := repoRoot(t)
	root := scenariosDir(t)
	scenarios := discoverScenarios(t)
	for _, name := range scenarios {
		name := name
		t.Run(name, func(t *testing.T) {
			fixture := filepath.Join(root, name)

			for _, manifest := range discoverManifests(t, fixture) {
				gen := exec.Command("go", "run", "./cmd/craftgo", "gen", "-f", manifest, "-c", filepath.Dir(manifest))
				gen.Dir = repo
				if out, err := gen.CombinedOutput(); err != nil {
					t.Fatalf("craftgo gen %s failed: %v\n%s", manifest, err, out)
				}
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
