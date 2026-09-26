package lsp

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

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
	if errs := designopts.FileErrors(v.diags, v.current); len(errs) > 0 {
		t.Errorf("a clean design must not block formatting: %+v", errs)
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

// A file that declares no package is reported, as the CLI reports it, and
// joins no package.
func TestAPackagelessFileIsReported(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), layoutOnly)
	named := filepath.Join(root, "design", "orders.craftgo")
	mustWrite(t, named, `package orders
type Order { id string }
`)
	extra := filepath.Join(root, "design", "extra.craftgo")
	mustWrite(t, extra, `type Extra { note string }
`)

	s := newTestServer()
	v := s.loadProject(named, readFileT(t, named))
	var missing []string
	for _, d := range v.diags {
		if d.Code == semantic.CodePackageMissing {
			missing = append(missing, d.Pos.Filename)
		}
	}
	if len(missing) != 1 || missing[0] != extra {
		t.Errorf("package/missing reported in %v, want [%s]", missing, extra)
	}
	if got := slices.Sorted(maps.Keys(v.proj.Packages)); len(got) != 1 || got[0] != "orders" {
		t.Errorf("packages = %v, want [orders]", got)
	}
}

// A file beside a design folder is analysed on its own, as `craftgo fmt`
// analyses it: the folder's declarations do not resolve in it.
func TestAFileOutsideTheDesignRootIsAnalysedAlone(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), layoutOnly)
	mustWrite(t, filepath.Join(root, "design", "app.craftgo"), "package app\n\ntype A { x string }\n")
	stray := filepath.Join(root, "stray.craftgo")
	mustWrite(t, stray, "package app\n\ntype B { a A }\n")
	v := newTestServer().loadProject(stray, readFileT(t, stray))
	if v.root != "" || len(v.files) != 1 {
		t.Errorf("root %q with %d file(s), want the file alone", v.root, len(v.files))
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
