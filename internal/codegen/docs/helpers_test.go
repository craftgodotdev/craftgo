package docs

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// updateGolden makes [expectGolden] rewrite the golden files:
// `go test ./internal/codegen/docs -update`.
var updateGolden = flag.Bool("update", false, "rewrite golden snapshot files instead of comparing")

func analyze(t *testing.T, src string) *semantic.Package {
	t.Helper()
	p := craftparser.New("test.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse errors: %v", d)
	}
	pkg, diags := semantic.Analyze([]*ast.File{f})
	// Only an error-severity diagnostic fails the test.
	var fatal []semantic.Diagnostic
	for _, d := range diags {
		if d.Severity == lexer.SeverityError {
			fatal = append(fatal, d)
		}
	}
	if len(fatal) > 0 {
		t.Fatalf("semantic errors: %v", fatal)
	}
	return pkg
}

// mustContainAll reports, in one error, every want missing from got.
func mustContainAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	var missing []string
	for _, w := range wants {
		if !strings.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Errorf("output missing %d expected substring(s):\n  - %s\n--- got ---\n%s",
			len(missing), strings.Join(missing, "\n  - "), got)
	}
}

func sampleConfig() *config.Config {
	return &config.Config{
		Package: "github.com/example/app",
		Output: config.Output{
			Types:      "./internal/types",
			Transport:  "./internal/transport",
			Routes:     "./internal/routes",
			Service:    "./internal/service",
			Svccontext: "./svccontext/svccontext.go",
			OpenAPI:    "./docs/openapi.yaml",
		},
		OpenAPI: config.OpenAPI{BasePath: "/v1"},
	}
}

// expectGolden compares actual with testdata/golden/<name>, or writes it
// there under -update.
func expectGolden(t *testing.T, name, actual string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata/golden: %v", err)
		}
		if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("wrote golden %s (%d bytes)", path, len(actual))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s does not exist; run with -update to create it", path)
		}
		t.Fatalf("read golden %s: %v", path, err)
	}
	// A Windows checkout may give the golden file CRLF line endings.
	wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")
	if wantStr == actual {
		return
	}
	t.Errorf("golden mismatch (%s) - diff first divergence:\n%s", path, firstDiff(wantStr, actual))
}

// projectFiles writes src under a temp dir and returns the dir and the
// parsed files.
func projectFiles(t *testing.T, src map[string]string) (string, []*ast.File) {
	t.Helper()
	root := t.TempDir()
	var files []*ast.File
	for rel, content := range src {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		p := craftparser.New(full, content)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse %s: %v", rel, d)
		}
		files = append(files, f)
	}
	return root, files
}

// firstDiff returns the lines around the first one where want and got
// differ.
func firstDiff(want, got string) string {
	wantLines := strings.SplitSeq(want, "\n")
	gotLines := strings.SplitSeq(got, "\n")
	wIter, gIter := wantLines, gotLines
	wantSlice := stringsCollect(wIter)
	gotSlice := stringsCollect(gIter)
	max := len(wantSlice)
	if len(gotSlice) > max {
		max = len(gotSlice)
	}
	for i := 0; i < max; i++ {
		w, g := "", ""
		if i < len(wantSlice) {
			w = wantSlice[i]
		}
		if i < len(gotSlice) {
			g = gotSlice[i]
		}
		if w != g {
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 4
			if end > max {
				end = max
			}
			var sb strings.Builder
			for j := start; j < end; j++ {
				marker := "  "
				if j == i {
					marker = "→ "
				}
				wj, gj := "", ""
				if j < len(wantSlice) {
					wj = wantSlice[j]
				}
				if j < len(gotSlice) {
					gj = gotSlice[j]
				}
				sb.WriteString(marker)
				sb.WriteString("want: ")
				sb.WriteString(wj)
				sb.WriteString("\n")
				sb.WriteString(marker)
				sb.WriteString("got:  ")
				sb.WriteString(gj)
				sb.WriteString("\n")
			}
			return sb.String()
		}
	}
	return "(strings differ in length but match line-by-line up to the shorter end)"
}

// stringsCollect drains a [strings.SplitSeq] iterator into a slice.
func stringsCollect(it func(yield func(string) bool)) []string {
	var out []string
	it(func(s string) bool {
		out = append(out, s)
		return true
	})
	return out
}

// mustContainNone reports, in one error, every unwanted string present in got.
func mustContainNone(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	var present []string
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			present = append(present, w)
		}
	}
	if len(present) > 0 {
		t.Errorf("output unexpectedly contains %d forbidden substring(s):\n  - %s\n--- got ---\n%s",
			len(present), strings.Join(present, "\n  - "), got)
	}
}

const handlerSampleDSL = `package design

type GetUserReq { id string }
type UpdateUserReq { id string  name string }
type User { id string  name string }

@prefix("/api/v1")
service UserService {
    get GetUser /users/{id} {
        request   GetUserReq
        response  User
    }
    post UpdateUser /users/{id} {
        request   UpdateUserReq
        response  User
    }
    delete DeleteUser /users/{id} {
        request   GetUserReq
        response  User
    }
}

extend service UserService {
    @doc("simple ping")
    get Ping {
    }
}`
