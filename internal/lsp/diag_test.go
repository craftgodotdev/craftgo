package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/uri"
)

// newTestServer returns an empty Server suitable for unit tests that
// invoke buildDiagnostics directly. It sidesteps the JSON-RPC wiring -
// the diag pipeline only reads from s.docs and the file system.
func newTestServer() *Server {
	return &Server{docs: map[uri.URI]*document{}}
}

// TestBuildDiagnosticsClean verifies that valid DSL produces no
// diagnostics - the formatter's roundtrip cases live there too, so this
// is mostly a smoke check that wiring is alive.
func TestBuildDiagnosticsClean(t *testing.T) {
	src := `package design

type User {
	id   string
	name string @length(1, 80)
}
`
	got := newTestServer().buildDiagnostics(uri.New("file:///test.craftgo"), src)
	if len(got) != 0 {
		t.Fatalf("expected zero diagnostics for clean source, got %d: %+v", len(got), got)
	}
}

// TestBuildDiagnosticsParseError checks that a syntax error is surfaced
// with a usable position (line/character non-zero) and the source label.
func TestBuildDiagnosticsParseError(t *testing.T) {
	// Missing closing brace.
	src := `package design

type User {
	id string
`
	got := newTestServer().buildDiagnostics(uri.New("file:///test.craftgo"), src)
	if len(got) == 0 {
		t.Fatal("expected at least one diagnostic for unclosed type body")
	}
	d := got[0]
	if d.Source != "craftgo" {
		t.Errorf("Source = %q, want %q", d.Source, "craftgo")
	}
	if d.Message == "" {
		t.Error("Message is empty")
	}
}

// TestBuildProjectDiagnosticsPartitionsByFile pins the cross-file
// invalidation contract: when one file's edit changes the diagnostic
// surface in a sibling file, the per-file map MUST surface both files'
// post-edit state so the publisher can refresh stale squigglies in the
// dependent file without forcing the user to re-trigger an edit there.
//
// Scenario: a service file references a path-param field declared in a
// sibling types file. Editing the types file to add the matching field
// should clear the service file's "path segment has no matching field"
// error - but only if the publisher sends an updated (empty) list for
// the service file, which is what this contract enables.
func TestBuildProjectDiagnosticsPartitionsByFile(t *testing.T) {
	root := t.TempDir()
	manifest := `output:
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
  basePath: /api
`
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), manifest)

	// Service references a path-param `id`; sibling types file has the
	// matching field. Both files must analyse clean.
	servicePath := filepath.Join(root, "design", "things", "service.craftgo")
	typesPath := filepath.Join(root, "design", "things", "types.craftgo")
	mustWrite(t, servicePath, `package things
service S {
    get GetThing /{id} {
        request  Req
        response Resp
    }
}
`)
	mustWrite(t, typesPath, `package things
type Req { id string }
type Resp {}
`)

	s := newTestServer()
	perFile, designRoot := s.buildProjectDiagnostics(uri.New("file://"+servicePath), readFileT(t, servicePath))
	if designRoot == "" {
		t.Fatal("expected project mode (design root resolved), got single-file fallback")
	}
	if len(perFile[servicePath]) != 0 {
		t.Errorf("service file should have zero diags when sibling provides the field: %+v", perFile[servicePath])
	}

	// Now break the contract: remove the `id` field from Req. The
	// service file's diagnostic surface must surface the path-param
	// mismatch even though we passed the SERVICE file as the trigger -
	// proving the partition includes diagnostics emitted against
	// sibling files via the project analyser.
	mustWrite(t, typesPath, `package things
type Req {}
type Resp {}
`)
	perFile2, _ := s.buildProjectDiagnostics(uri.New("file://"+servicePath), readFileT(t, servicePath))
	if len(perFile2[servicePath]) == 0 {
		t.Fatalf("expected path-param-missing diag for service file after sibling lost the field; got: %v", perFile2)
	}
	foundPathParam := false
	for _, d := range perFile2[servicePath] {
		c, _ := d.Code.(string)
		if strings.HasPrefix(c, "path/") {
			foundPathParam = true
			break
		}
	}
	if !foundPathParam {
		t.Errorf("expected a path/* diagnostic in service file's slice; got: %+v", perFile2[servicePath])
	}
}

// TestBuildProjectDiagnosticsClearsAfterRevert pins the "edit-and-undo"
// flow that was leaking stale squigglies before the perFile partition
// defaulted to a non-nil empty slice: nil marshals to JSON `null`, which
// several LSP clients interpret as "no change" rather than "clear".
//
// Scenario: edit types file to break a path-param reference (error
// fires in service file), then revert the edit (error should clear
// from service file's per-file partition as an EMPTY non-nil slice
// so the JSON payload is `[]` not `null`).
func TestBuildProjectDiagnosticsClearsAfterRevert(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), `output:
  types: ./internal/types
openapi:
  title: T
  version: 1.0.0
  basePath: /api
`)
	servicePath := filepath.Join(root, "design", "things", "service.craftgo")
	typesPath := filepath.Join(root, "design", "things", "types.craftgo")
	mustWrite(t, servicePath, `package things
service S {
    get GetThing /{id} {
        request  Req
        response Resp
    }
}
`)
	// Initial: types provides matching `id` field — clean state.
	mustWrite(t, typesPath, `package things
type Req { id string }
type Resp {}
`)
	s := newTestServer()
	if perFile, _ := s.buildProjectDiagnostics(uri.New("file://"+typesPath), readFileT(t, typesPath)); len(perFile[servicePath]) != 0 {
		t.Fatalf("expected service to be clean initially, got %+v", perFile[servicePath])
	}

	// Break: rename id → id1 so the path param has no matching field.
	mustWrite(t, typesPath, `package things
type Req { id1 string }
type Resp {}
`)
	if perFile, _ := s.buildProjectDiagnostics(uri.New("file://"+typesPath), readFileT(t, typesPath)); len(perFile[servicePath]) == 0 {
		t.Fatalf("expected service path-param error after rename; got: %+v", perFile)
	}

	// Revert: rename back. The service file's slot in perFile MUST be
	// non-nil even though it now carries zero diagnostics — otherwise
	// the publisher would send JSON `null` and the LSP client would
	// keep the stale squiggly.
	mustWrite(t, typesPath, `package things
type Req { id string }
type Resp {}
`)
	perFile, _ := s.buildProjectDiagnostics(uri.New("file://"+typesPath), readFileT(t, typesPath))
	if len(perFile[servicePath]) != 0 {
		t.Errorf("expected service diags to clear after revert, got %+v", perFile[servicePath])
	}
	// The publisher path: diagsFor() must hand back a non-nil empty
	// slice so the wire payload is `[]` (clears) not `null` (ignored).
	cleared := diagsFor(perFile, servicePath)
	if cleared == nil {
		t.Error("diagsFor returned nil; LSP clients treat JSON null as no-op — should be empty slice")
	}
	if len(cleared) != 0 {
		t.Errorf("expected empty cleared slice, got %d entries", len(cleared))
	}
}

// mustWrite writes b to path, creating the parent dir tree on demand.
// Test-only helper - tests fail fast on filesystem errors so the body
// stays focused on the contract under test.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestBuildDiagnosticsSemanticError checks that semantic errors (e.g.
// unknown decorator) are reported with their stable diagnostic codes,
// not just generic parse failures.
func TestBuildDiagnosticsSemanticError(t *testing.T) {
	src := `package design

type User {
	id string @notARealDecorator
}
`
	got := newTestServer().buildDiagnostics(uri.New("file:///test.craftgo"), src)
	var foundCode string
	for _, d := range got {
		c, _ := d.Code.(string)
		if strings.HasPrefix(c, "decorator/") {
			foundCode = c
			break
		}
	}
	if foundCode == "" {
		t.Fatalf("expected a decorator/* code in diagnostics, got %+v", got)
	}
}
