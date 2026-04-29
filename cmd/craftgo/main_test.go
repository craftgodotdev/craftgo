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
		"design/user.craftgo",
		"design/user-service.craftgo",
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
	dest := filepath.Join(dir, "design/user.craftgo")
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

// TestRunGenMultiPackage drives the full `craftgo gen` pipeline against
// a hand-written project that uses a subpackage (`design/shared/`) for
// shared domain types. It asserts:
//
//   - the root package's types.go imports the shared subpackage's Go
//     module and references types via the qualified `shared.User` form;
//   - the shared subpackage gets its own types.go in
//     `internal/types/shared/`;
//   - the generator does not reject services in the root, even when
//     fields cross package boundaries.
//
// A failure here means the multi-package codegen wiring regressed
// somewhere between AnalyzeProject, BuildCrossPkg, and the
// per-package generator entry points.
func TestRunGenMultiPackage(t *testing.T) {
	dir := t.TempDir()

	// Manifest at the design root.
	mustWrite(t, dir, "design/craftgo.design.yaml", `package: github.com/test/multi
design: ./design
`)

	// Root-package files: declare service + a request type that
	// references the sibling package's User.
	mustWrite(t, dir, "design/api.craftgo", `package design
import "shared"

type Login {
    user shared.User
    note string
}

service Auth {
    post DoLogin /login {
        request   Login
        response  Login
    }
}
`)

	// Sibling subpackage with its own User type.
	mustWrite(t, dir, "design/shared/user.craftgo", `package shared
type User {
    id   string
    name string
}
`)

	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}

	rootTypes, err := os.ReadFile(filepath.Join(dir, "internal/types/design/types.go"))
	if err != nil {
		t.Fatalf("read root types.go: %v", err)
	}
	if !strings.Contains(string(rootTypes), "shared.User") {
		t.Errorf("root types.go missing `shared.User` reference:\n%s", rootTypes)
	}
	if !strings.Contains(string(rootTypes), `"github.com/test/multi/internal/types/shared"`) {
		t.Errorf("root types.go missing Go import for sibling package:\n%s", rootTypes)
	}

	subTypes, err := os.ReadFile(filepath.Join(dir, "internal/types/shared/types.go"))
	if err != nil {
		t.Fatalf("read sibling types.go: %v", err)
	}
	if !strings.Contains(string(subTypes), "package shared") {
		t.Errorf("sibling types.go missing `package shared`:\n%s", subTypes)
	}
	if !strings.Contains(string(subTypes), "type User struct") {
		t.Errorf("sibling types.go missing User decl:\n%s", subTypes)
	}
}

// TestRunGenSubpackageService verifies multi-package projects can
// declare services in any package. Each package's services generate
// their own handlers/routes; the umbrella routes.RegisterAll
// aggregates services from every package; the merged OpenAPI
// document carries paths from all of them.
func TestRunGenSubpackageService(t *testing.T) {
	dir := t.TempDir()

	mustWrite(t, dir, "design/craftgo.design.yaml", `package: github.com/test/multi
design: ./design
`)
	mustWrite(t, dir, "design/api.craftgo", `package design
type Probe { id string }
service ProbeService {
    get Ping /ping {
        response   Probe
    }
}
`)
	mustWrite(t, dir, "design/auth/auth.craftgo", `package auth
type Cred { token string }
service AuthService {
    post Login /login {
        request   Cred
        response  Cred
    }
}
`)

	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}

	// Per-service handler dirs exist for both services.
	for _, rel := range []string{
		"internal/handler/probe-service/ping-handler.go",
		"internal/handler/auth-service/login-handler.go",
		"internal/types/design/types.go",
		"internal/types/auth/types.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// Umbrella routes.go imports BOTH service-route packages.
	umbrella, err := os.ReadFile(filepath.Join(dir, "internal/routes/routes.go"))
	if err != nil {
		t.Fatalf("read umbrella: %v", err)
	}
	if !strings.Contains(string(umbrella), "probe-service") {
		t.Errorf("umbrella missing probe-service import:\n%s", umbrella)
	}
	if !strings.Contains(string(umbrella), "auth-service") {
		t.Errorf("umbrella missing auth-service import:\n%s", umbrella)
	}

	// OpenAPI carries paths from BOTH services.
	spec, err := os.ReadFile(filepath.Join(dir, "docs/openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	if !strings.Contains(string(spec), "/ping") {
		t.Errorf("openapi missing /ping route:\n%s", spec)
	}
	if !strings.Contains(string(spec), "/login") {
		t.Errorf("openapi missing /login route:\n%s", spec)
	}
}

// TestRunGenCrossPackageRequestResponse verifies that a service
// declaring `request shared.Cred` (cross-package) gets handler and
// logic files that:
//
//   - import the sibling package's Go path;
//   - reference the type via the package's own alias rather than the
//     canonical `types` alias of the local package;
//   - drop the now-unused canonical `types` import when neither side
//     of the signature is local.
//
// This is the "v1 constraint #1" relaxation: services in any package
// may reference request/response types from any other package.
func TestRunGenCrossPackageRequestResponse(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "design/craftgo.design.yaml", `package: github.com/test/cross
design: ./design
`)
	mustWrite(t, dir, "design/api.craftgo", `package design
import "shared"

service Auth {
    post Login /login {
        request   shared.Cred
        response  shared.Token
    }
}
`)
	mustWrite(t, dir, "design/shared/types.craftgo", `package shared
type Cred  { user string  pass string }
type Token { value string }
`)

	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}

	handler, err := os.ReadFile(filepath.Join(dir, "internal/handler/auth/login-handler.go"))
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	hs := string(handler)
	if !strings.Contains(hs, `shared "github.com/test/cross/internal/types/shared"`) {
		t.Errorf("handler missing cross-pkg import:\n%s", hs)
	}
	if !strings.Contains(hs, "var req shared.Cred") {
		t.Errorf("handler missing `var req shared.Cred`:\n%s", hs)
	}
	if strings.Contains(hs, `types "`) {
		t.Errorf("handler should not import the canonical types alias when request is cross-pkg:\n%s", hs)
	}

	logic, err := os.ReadFile(filepath.Join(dir, "internal/logic/auth/login-logic.go"))
	if err != nil {
		t.Fatalf("read logic: %v", err)
	}
	ls := string(logic)
	if !strings.Contains(ls, `shared "github.com/test/cross/internal/types/shared"`) {
		t.Errorf("logic missing cross-pkg import:\n%s", ls)
	}
	if !strings.Contains(ls, "(req *shared.Cred)") {
		t.Errorf("logic signature wrong; want `req *shared.Cred`:\n%s", ls)
	}
	if !strings.Contains(ls, "*shared.Token") {
		t.Errorf("logic response type wrong:\n%s", ls)
	}
	if strings.Contains(ls, `types "`) {
		t.Errorf("logic should not import the canonical types alias when both sides cross-pkg:\n%s", ls)
	}
}

// mustWrite is a tiny helper that writes path with intermediate dirs.
// Centralised because the multi-package tests build small fixtures and
// the inline `MkdirAll + WriteFile` boilerplate clutters the assertion
// logic.
func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
