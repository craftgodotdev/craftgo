package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunInitWritesScaffold checks the happy path: a fresh directory ends
// up with all four starter files, the manifest carries the supplied
// `-package` value, and the sample DSL parses end-to-end via the existing
// `runGen` driver. We don't separately re-test the generator; we trust
// that the gen tests cover the codegen surface and only assert that the
// init output is valid input to it.
func TestRunInitWritesScaffold(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{dir, "-package", "github.com/test/app"}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	want := []string{
		"design/craftgo.design.yaml",
		"design/api.craftgo",
		"design/types/user.craftgo",
		"design/services/user-service.craftgo",
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	manifest, _ := os.ReadFile(filepath.Join(dir, "design/craftgo.design.yaml"))
	if !strings.Contains(string(manifest), "package: github.com/test/app") {
		t.Errorf("manifest missing custom package path:\n%s", manifest)
	}

	// Generated scaffold must be a valid input to `craftgo gen`.
	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen on scaffold: %v", err)
	}
	for _, rel := range []string{
		"main.go",
		"internal/types/design/types.go",
		"internal/handler/user-service/get-user-handler.go",
		"docs/openapi.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing generated %s: %v", rel, err)
		}
	}
}

// TestRunInitIdempotent guarantees that re-running init does not clobber
// existing files. The user is allowed to edit any starter file, then
// re-run init to fill in newly added templates without losing edits.
func TestRunInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{dir}); err != nil {
		t.Fatal(err)
	}
	custom := "// USER EDIT — must survive re-init"
	dest := filepath.Join(dir, "design/types/user.craftgo")
	if err := os.WriteFile(dest, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInit([]string{dir}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != custom {
		t.Errorf("user edit was overwritten; got:\n%s", got)
	}
}

// TestRunInitDefaultPath asserts that with no positional path argument
// the command writes into the current working directory. We chdir into a
// temp dir so the test doesn't pollute the repository.
func TestRunInitDefaultPath(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := runInit(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design/craftgo.design.yaml")); err != nil {
		t.Errorf("default-path init did not write manifest: %v", err)
	}
}

// TestRunInitRejectsUnknownFlag ensures typos surface immediately rather
// than silently being treated as a path.
func TestRunInitRejectsUnknownFlag(t *testing.T) {
	if err := runInit([]string{"--bogus"}); err == nil {
		t.Error("expected error for unknown flag")
	}
}

// TestRunInitMissingPackageValue verifies the trailing `-package` with no
// argument is rejected rather than silently using the empty string.
func TestRunInitMissingPackageValue(t *testing.T) {
	if err := runInit([]string{"-package"}); err == nil {
		t.Error("expected error for missing -package value")
	}
}
