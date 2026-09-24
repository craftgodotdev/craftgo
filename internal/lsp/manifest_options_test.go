package lsp

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// manifestProject writes a design root with the given manifest body and
// one design file, and returns the file's path.
func manifestProject(t *testing.T, manifest, design string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), manifest)
	path := filepath.Join(root, "design", "svc.craftgo")
	mustWrite(t, path, design)
	return path
}

const layoutOnly = `output:
  types:      ./internal/types
  transport:  ./internal/transport
  routes:     ./internal/routes
  service:    ./internal/service
  middleware: ./internal/middleware
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
openapi:
  title: T
  version: 1.0.0
`

// The manifest's basePath moves `/healthz` to `/api/healthz`, clearing the
// health conflict; without a basePath the conflict is reported.
func TestABasePathSilencesTheHealthConflictInTheEditorToo(t *testing.T) {
	const design = `package svc
type R {}
service S {
    get Health /healthz { response R }
}
`
	withBase := manifestProject(t, layoutOnly+"  basePath: /api\n", design)
	s := newTestServer()
	v := s.loadProject(withBase, readFileT(t, withBase))
	for _, d := range v.diags {
		if d.Code == "path/health-conflict" {
			t.Errorf("basePath /api moves the route to /api/healthz, so there is no conflict: %s", d.Msg)
		}
	}
	if v.hasErrors() {
		t.Errorf("a clean design must not block formatting: %+v", v.diags)
	}

	// Without a basePath the route is /healthz.
	noBase := manifestProject(t, layoutOnly, design)
	v = s.loadProject(noBase, readFileT(t, noBase))
	found := false
	for _, d := range v.diags {
		if d.Code == "path/health-conflict" {
			found = true
		}
	}
	if !found {
		t.Error("with no basePath /healthz does collide and must still be reported")
	}
}

// `@security(name)` is checked against the manifest's declared schemes.
func TestTheEditorChecksSecuritySchemesAgainstTheManifest(t *testing.T) {
	manifest := layoutOnly + `  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
    apiKeyAuth:
      type: apiKey
      in: header
      name: X-Key
`
	path := manifestProject(t, manifest, `package svc
type R {}
service S {
    @security(nope)
    get Read /r { response R }
}
`)
	s := newTestServer()
	v := s.loadProject(path, readFileT(t, path))

	var msg string
	for _, d := range v.diags {
		if d.Code == "decorator/ref" {
			msg = d.Msg
		}
	}
	if msg == "" {
		t.Fatalf("an undeclared scheme must be reported in the editor: %+v", v.diags)
	}
	// The message lists the known schemes sorted.
	if !strings.Contains(msg, `"apiKeyAuth", "bearerAuth"`) {
		t.Errorf("known schemes are not sorted: %s", msg)
	}
}

// Outside a project `@security(name)` is not checked: there is no scheme list.
func TestASchemeReferenceOutsideAProjectIsNotReported(t *testing.T) {
	s := newTestServer()
	v := s.loadProject("", `package svc
type R {}
service S {
    @security(anything)
    get Read /r { response R }
}
`)
	for _, d := range v.diags {
		if d.Code == "decorator/ref" {
			t.Errorf("a buffer outside a project has no scheme list to check against: %s", d.Msg)
		}
	}
}

// A file that declares no package joins the project's only named package.
func TestAPackagelessFileJoinsTheOnlyNamedPackage(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), layoutOnly)
	named := filepath.Join(root, "design", "orders.craftgo")
	mustWrite(t, named, `package orders
type Order { id string  extra Extra }
`)
	mustWrite(t, filepath.Join(root, "design", "extra.craftgo"), `type Extra { note string }
`)

	s := newTestServer()
	v := s.loadProject(named, readFileT(t, named))
	for _, d := range v.diags {
		if d.IsError() {
			t.Errorf("the package-less file's type must resolve, as it does for the CLI: %s at %s", d.Msg, d.Pos)
		}
	}
	if _, ok := v.proj.Packages["orders"]; !ok {
		t.Fatalf("packages = %v, want the one named package", slices.Sorted(maps.Keys(v.proj.Packages)))
	}
	if len(v.proj.Packages) != 1 {
		t.Errorf("packages = %v, want exactly one - naming a package-less file after its folder splits the project",
			slices.Sorted(maps.Keys(v.proj.Packages)))
	}
}

// designProjectOf returns the manifest with the root, and neither for an
// untitled buffer.
func TestDesignProjectOfReturnsTheManifestWithTheRoot(t *testing.T) {
	path := manifestProject(t, layoutOnly+"  basePath: /api\n", "package svc\ntype R {}\n")

	cfg, root := designProjectOf(path)
	if root == "" || cfg == nil {
		t.Fatalf("designProjectOf(%s) = %v, %q", path, cfg, root)
	}
	if cfg.OpenAPI.BasePath != "/api" {
		t.Errorf("basePath = %q, want /api", cfg.OpenAPI.BasePath)
	}

	if cfg, root := designProjectOf(""); cfg != nil || root != "" {
		t.Errorf("an untitled buffer = %v, %q; want nil and empty", cfg, root)
	}
}

// An unreadable directory does not hide the design files after it.
func TestAnUnreadableDirectoryDoesNotInventUnknownSymbols(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), layoutOnly)
	user := filepath.Join(root, "design", "aaa", "a.craftgo")
	mustWrite(t, user, `package aaa
type Uses { z ccc.Zed }
`)
	mustWrite(t, filepath.Join(root, "design", "bbb", "b.craftgo"), "package bbb\n")
	mustWrite(t, filepath.Join(root, "design", "ccc", "c.craftgo"), `package ccc
type Zed { id string }
`)

	blocked := filepath.Join(root, "design", "bbb")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Skipf("cannot make %s unreadable: %v", blocked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("directory is still readable (running as root?)")
	}

	s := newTestServer()
	v := s.loadProject(user, readFileT(t, user))
	for _, d := range v.diags {
		if d.IsError() {
			t.Errorf("the editor invented %q at %s - ccc is readable and declares Zed", d.Msg, d.Pos)
		}
	}
}
