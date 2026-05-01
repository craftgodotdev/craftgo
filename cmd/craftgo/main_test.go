package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunInitWritesScaffold checks the happy path: the supplied path IS
// the design folder, the manifest lands flat inside it, and (after the
// user supplies a go.mod and a minimal DSL) the result drives
// `craftgo gen` end-to-end. We don't separately re-test the generator;
// we trust the gen tests cover that surface and only assert that the
// init output is a valid starting point for it.
func TestRunInitWritesScaffold(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "contracts", "v1")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// Init writes ONLY the manifest — sample DSL stays the user's
	// responsibility so they don't have to delete noise on day one.
	if _, err := os.Stat(filepath.Join(designFolder, "craftgo.design.yaml")); err != nil {
		t.Errorf("missing manifest: %v", err)
	}
	manifest, _ := os.ReadFile(filepath.Join(designFolder, "craftgo.design.yaml"))
	for _, line := range strings.Split(string(manifest), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "package:") {
			t.Errorf("manifest must NOT carry a `package:` field; module path now lives in go.mod:\n%s", manifest)
			break
		}
	}

	// Drop a minimal user-written DSL file + go.mod so the gen
	// pipeline has the inputs it needs (DSL for codegen, go.mod for
	// the module path). The project root in this test is `dir`.
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	mustWrite(t, dir, "go.mod", "module github.com/test/app\n\ngo 1.24\n")

	if err := runGen([]string{"-f", designFolder, "-c", dir}); err != nil {
		t.Fatalf("runGen on scaffold: %v", err)
	}
	for _, rel := range []string{
		"main.go",
		"internal/types/api/types.go",
		"internal/handler/probe-service/ping-handler.go",
		"docs/openapi.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing generated %s: %v", rel, err)
		}
	}
}

// TestRunInitIdempotent guarantees that re-running init does not clobber
// the existing manifest — the user may have edited it (changed the
// package path, added security schemes, ...) and a second init must
// preserve those edits.
func TestRunInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{dir}); err != nil {
		t.Fatal(err)
	}
	custom := "# USER EDIT — must survive re-init\npackage: github.com/edited/app\n"
	dest := filepath.Join(dir, "craftgo.design.yaml")
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

// minimalDesignDSL is the smallest .craftgo file that exercises every
// downstream gen step (types + service + route). Tests that need to
// drive `runGen` after `runInit` drop this in the design folder so
// they don't depend on init scaffolding sample DSL.
const minimalDesignDSL = `package api

type Probe { id string }

service ProbeService {
    get Ping /ping {
        response   Probe
    }
}
`

// TestRunInitDefaultPath asserts that with no positional path argument
// the command creates a `design/` subdir of cwd — the conventional
// layout for fresh projects. We chdir into a temp dir so the test
// doesn't pollute the repository.
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
	if _, err := os.Stat(filepath.Join(dir, "design", "craftgo.design.yaml")); err != nil {
		t.Errorf("default-path init did not write manifest: %v", err)
	}
}

// TestRunGenContextOverridesProjectRoot confirms `-c` redirects
// outputs to the supplied root regardless of where the manifest
// lives. Common monorepo shape: contracts/ holds design, services/
// holds the generated code. The single shared go.mod at the repo
// root is the canonical "monorepo with one module" layout, and
// ResolveModulePath walks up from -c to find it.
func TestRunGenContextOverridesProjectRoot(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "contracts", "v1")
	codeRoot := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(codeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "go.mod", "module github.com/test/monorepo\n\ngo 1.24\n")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	if err := runGen([]string{"-f", designFolder, "-c", codeRoot}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codeRoot, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("expected types under -c root, got: %v", err)
	}
	// The shared-go.mod monorepo layout: imports are
	// <module>/<relPath> = github.com/test/monorepo/services/api/...
	types, _ := os.ReadFile(filepath.Join(codeRoot, "internal", "types", "api", "types.go"))
	if !strings.Contains(string(types), "package api") {
		t.Errorf("generated types.go missing package decl:\n%s", types)
	}
}

// TestRunGenWalkUpKeepsLegacyProjectRoot pins the legacy positional
// flow — `craftgo gen <path>` keeps using parent-of-manifest as the
// project root so existing fixtures (example/, testdata/e2e/*) keep
// working without flag changes.
func TestRunGenWalkUpKeepsLegacyProjectRoot(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "design")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	mustWrite(t, dir, "go.mod", "module github.com/test/legacy\n\ngo 1.24\n")
	// Positional path = dir; walk-up finds dir/design/manifest.
	// projectRoot stays at dir (parent of manifest), NOT cwd.
	if err := runGen([]string{dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "types", "api", "types.go")); err != nil {
		t.Errorf("legacy walk-up should land outputs at parent-of-manifest: %v", err)
	}
}

// TestRunGenMissingGoMod pins the fail-fast contract: gen MUST refuse
// to run when no go.mod can be located, with a clear error message
// pointing the user at `go mod init`.
func TestRunGenMissingGoMod(t *testing.T) {
	dir := t.TempDir()
	designFolder := filepath.Join(dir, "design")
	if err := runInit([]string{designFolder}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	mustWrite(t, designFolder, "api.craftgo", minimalDesignDSL)
	err := runGen([]string{dir})
	if err == nil {
		t.Fatal("expected error when go.mod is missing")
	}
	if !strings.Contains(err.Error(), "go mod init") {
		t.Errorf("error must point at the fix; got: %v", err)
	}
}

// TestRunInitRejectsUnknownFlag ensures typos surface immediately rather
// than silently being treated as a path.
func TestRunInitRejectsUnknownFlag(t *testing.T) {
	if err := runInit([]string{"--bogus"}); err == nil {
		t.Error("expected error for unknown flag")
	}
}

// TestRunInitRejectsLegacyPackageFlag pins the removal of `-package`:
// the module path now lives in go.mod, not the manifest, so the flag
// no longer exists. Old scripts that still pass it must surface a
// clear error rather than silently accept it.
func TestRunInitRejectsLegacyPackageFlag(t *testing.T) {
	if err := runInit([]string{"-package", "github.com/test/app"}); err == nil {
		t.Error("expected error for removed -package flag")
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

	// go.mod at the project root supplies the module path the
	// generated cross-package imports must reference.
	mustWrite(t, dir, "go.mod", "module github.com/test/multi\n\ngo 1.24\n")
	// Manifest at the design root. No `package:` field — the module
	// path comes from go.mod above.
	mustWrite(t, dir, "design/craftgo.design.yaml", "")

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

	mustWrite(t, dir, "go.mod", "module github.com/test/multi\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
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
	mustWrite(t, dir, "go.mod", "module github.com/test/cross\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
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
