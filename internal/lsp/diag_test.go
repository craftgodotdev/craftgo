package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// newTestServer returns a server with no open documents and no connection.
func newTestServer() *server {
	return &server{docs: map[uri.URI]string{}}
}

// bufferDiagnostics returns the diagnostics of src open outside any project.
func bufferDiagnostics(src string) []protocol.Diagnostic {
	u := uri.New("file:///t.craftgo")
	perFile, _ := newTestServer().buildProjectDiagnostics(u, src)
	return perFile[uriToPath(string(u))]
}

// A related location in an untitled buffer points at the buffer.
func TestRelatedInformationInAnUntitledBuffer(t *testing.T) {
	u := uri.URI("untitled:Untitled-1")
	perFile, _ := newTestServer().buildProjectDiagnostics(u, "package x\n\ntype A {}\ntype A {}\n")
	related := 0
	for _, d := range perFile[""] {
		for _, r := range d.RelatedInformation {
			related++
			if r.Location.URI != u {
				t.Errorf("related location in %q, want the buffer's %q", r.Location.URI, u)
			}
		}
	}
	if related == 0 {
		t.Fatalf("no related information in %+v", perFile)
	}
}

// Valid source produces no diagnostics.
func TestBuildDiagnosticsClean(t *testing.T) {
	src := `package design

type User {
	id   string
	name string @length(1, 80)
}
`
	got := bufferDiagnostics(src)
	if len(got) != 0 {
		t.Fatalf("expected zero diagnostics for clean source, got %d: %+v", len(got), got)
	}
}

// A syntax error is reported with the craftgo source and a message.
func TestBuildDiagnosticsParseError(t *testing.T) {
	// Missing closing brace.
	src := `package design

type User {
	id string
`
	got := bufferDiagnostics(src)
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

// A change in one file changes the diagnostics reported for its sibling.
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

	// The service's path parameter `id` matches a field in the types file.
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
	perFile, designRoot := s.buildProjectDiagnostics(uri.File(servicePath), readFileT(t, servicePath))
	if designRoot == "" {
		t.Fatal("expected project mode (design root resolved), got single-file fallback")
	}
	if len(perFile[servicePath]) != 0 {
		t.Errorf("service file should have zero diags when sibling provides the field: %+v", perFile[servicePath])
	}

	// Without Req.id the service file reports its path parameter.
	mustWrite(t, typesPath, `package things
type Req {}
type Resp {}
`)
	perFile2, _ := s.buildProjectDiagnostics(uri.File(servicePath), readFileT(t, servicePath))
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

// Reverting a breaking edit clears the sibling's diagnostics to `[]`.
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
	// Clean: Req has the `id` field.
	mustWrite(t, typesPath, `package things
type Req { id string }
type Resp {}
`)
	s := newTestServer()
	if perFile, _ := s.buildProjectDiagnostics(uri.File(typesPath), readFileT(t, typesPath)); len(perFile[servicePath]) != 0 {
		t.Fatalf("expected service to be clean initially, got %+v", perFile[servicePath])
	}

	// Break: rename id to id1.
	mustWrite(t, typesPath, `package things
type Req { id1 string }
type Resp {}
`)
	if perFile, _ := s.buildProjectDiagnostics(uri.File(typesPath), readFileT(t, typesPath)); len(perFile[servicePath]) == 0 {
		t.Fatalf("expected service path-param error after rename; got: %+v", perFile)
	}

	// Revert: rename back.
	mustWrite(t, typesPath, `package things
type Req { id string }
type Resp {}
`)
	perFile, _ := s.buildProjectDiagnostics(uri.File(typesPath), readFileT(t, typesPath))
	if len(perFile[servicePath]) != 0 {
		t.Errorf("expected service diags to clear after revert, got %+v", perFile[servicePath])
	}
	// diagsFor returns an empty, non-nil list: clients ignore null.
	cleared := diagsFor(perFile, servicePath)
	if cleared == nil {
		t.Error("diagsFor returned nil; LSP clients treat JSON null as no-op - should be empty slice")
	}
	if len(cleared) != 0 {
		t.Errorf("expected empty cleared slice, got %d entries", len(cleared))
	}
}

// mustWrite writes content to path, creating its parent directories.
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

// An unknown decorator is reported with a decorator/* code.
func TestBuildDiagnosticsSemanticError(t *testing.T) {
	src := `package design

type User {
	id string @notARealDecorator
}
`
	got := bufferDiagnostics(src)
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

// `@format(raw)` on a string field is reported as decorator/typemismatch on
// that row, while the bytes field beside it is accepted.
func TestBuildDiagnosticsFormatRawOffBytes(t *testing.T) {
	src := `package design

type Hook {
	ok      bytes @format(raw)
	payload string @format(raw)
}
`
	got := bufferDiagnostics(src)
	if len(got) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %+v", len(got), got)
	}
	d := got[0]
	if d.Code != "decorator/typemismatch" {
		t.Errorf("Code = %v, want decorator/typemismatch", d.Code)
	}
	if !strings.Contains(d.Message, "@format(raw) applies to bytes") ||
		!strings.Contains(d.Message, "is string") {
		t.Errorf("Message = %q", d.Message)
	}
	if d.Range.Start.Line != 4 {
		t.Errorf("the squiggly sits on line %d, want the `payload` row (4)", d.Range.Start.Line)
	}
}
