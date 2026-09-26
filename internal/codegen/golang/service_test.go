package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateServiceScaffold(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	cfg := sampleConfig()
	if err := generateService(pkg, cfg, root, nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "internal/service/user-service")
	for _, fn := range []string{"get-user.go", "update-user.go", "ping.go"} {
		out, err := os.ReadFile(filepath.Join(dir, fn))
		if err != nil {
			t.Fatalf("missing %s: %v", fn, err)
		}
		mustParseGo(t, string(out))
	}
	pingSrc, _ := os.ReadFile(filepath.Join(dir, "ping.go"))
	if !strings.Contains(string(pingSrc), "func (l *PingService) Ping() error {") {
		t.Errorf("Ping logic signature mismatch:\n%s", pingSrc)
	}
	getSrc, _ := os.ReadFile(filepath.Join(dir, "get-user.go"))
	if !strings.Contains(string(getSrc), "func (l *GetUserService) GetUser(req *types.GetUserReq) (*types.User, error)") {
		t.Errorf("GetUser logic signature mismatch:\n%s", getSrc)
	}
}

// A service stub renders generic request and response types with their type arguments.
func TestGenerateServiceGenericInstantiation(t *testing.T) {
	src := `package design
type User { id string }
scalar Email string @format(email)
type Page<T> { items T[]  total int }
type Envelope<T> { data T }
type Pair<A, B> { left A  right B }
type CreateReq { user User }
type EchoReq { v string }
type WrapReq { v string }
type PairReq { v string }
type GridReq { v string }
service S {
    post Create /c   { request CreateReq  response Page<User> }
    post Echo   /e   { request EchoReq    response Envelope<Email> }
    post Wrap   /w   { request WrapReq    response Page<Envelope<User>> }
    post Pair   /p   { request PairReq    response Pair<User, Email> }
    post Mix    /m   { request Page<User> response Envelope<User> }
    post Grid   /g   { request GridReq    response Page<map<string, User>[]> }
}`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := generateService(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		file, want string
	}{
		// Single-arg generic with local type.
		{"create.go", "(*types.Page[types.User], error)"},
		// Single-arg generic with local scalar.
		{"echo.go", "(*types.Envelope[types.Email], error)"},
		// Nested generic.
		{"wrap.go", "(*types.Page[types.Envelope[types.User]], error)"},
		// Multi-arg generic mixing struct + scalar.
		{"pair.go", "(*types.Pair[types.User, types.Email], error)"},
		// Generic on the request side too.
		{"mix.go", "(req *types.Page[types.User])"},
		{"mix.go", "(*types.Envelope[types.User], error)"},
		// An array-of-maps argument keeps its `[]`.
		{"grid.go", "(*types.Page[[]map[string]types.User], error)"},
	}
	for _, c := range cases {
		body, err := os.ReadFile(filepath.Join(root, "internal/service/s", c.file))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		got := string(body)
		mustParseGo(t, got)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s missing %q:\n%s", c.file, c.want, got)
		}
	}
}

// A service stub that already exists is left as it is.
func TestGenerateServiceSkipsExisting(t *testing.T) {
	pkg := analyze(t, handlerSampleDSL)
	root := t.TempDir()
	dir := filepath.Join(root, "internal/service/user-service")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "get-user.go")
	custom := []byte("package userservice\n// user-owned\n")
	if err := os.WriteFile(existing, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generateService(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	if out := readGen(t, dir, "get-user.go"); out != string(custom) {
		t.Errorf("scaffold overwrote user file:\n%s", out)
	}
	// The other stubs land beside it, so dir is where gen writes.
	readGen(t, dir, "update-user.go")
}
