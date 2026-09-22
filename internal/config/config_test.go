package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny test helper that writes content to path with default
// permissions and fails the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadDefaults pins the empty-manifest behaviour: with no keys set
// every Output.* path falls back to its framework default. Package is
// not a manifest field - it is resolved from go.mod at gen time - so an
// empty manifest is a valid input.
func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.Types != "./internal/types" {
		t.Errorf("default types: %s", cfg.Output.Types)
	}
	if cfg.Output.Main != "./main.go" {
		t.Error("default main")
	}
	if cfg.Output.Wiring != "./internal/wiring" {
		t.Errorf("output.wiring default = %q", cfg.Output.Wiring)
	}
	if cfg.Output.Config != "./config" {
		t.Error("default config")
	}
}

func TestLoadFullOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, `output:
  types: ./gen/types
  main: ./cmd/api/main.go
  svccontext: ./internal/svc/svccontext.go
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.Types != "./gen/types" {
		t.Error()
	}
	if cfg.Output.Main != "./cmd/api/main.go" {
		t.Error()
	}
	if cfg.Output.Svccontext != "./internal/svc/svccontext.go" {
		t.Error()
	}
}

// TestLoadIgnoresStrayPackageKey: a `package:` key in the manifest
// is ignored (no struct field consumes it) so such manifests load
// cleanly. The sole source of truth for the module path is go.mod.
func TestLoadIgnoresStrayPackageKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "package: github.com/old/manifest\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("legacy manifest with stray package: should still load: %v", err)
	}
	if cfg.Package != "" {
		t.Errorf("Package must be empty after Load (set later by ResolveModulePath); got %q", cfg.Package)
	}
}

func TestLoadBadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "not: valid: yaml: at: all\n  bad")
	if _, err := Load(path); err == nil {
		t.Error("expected parse error")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/no/such/path/craftgo.design.yaml"); err == nil {
		t.Error("expected error")
	}
}

// TestFindManifestInsideDesignFolder is the canonical layout: the manifest
// lives inside `design/`, and Find walks up from a sibling source dir to
// locate it.
func TestFindManifestInsideDesignFolder(t *testing.T) {
	root := t.TempDir()
	designDir := filepath.Join(root, "design")
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(designDir, Filename), "")
	// Walk from a deep sibling - Find should still discover design/ via
	// the project root.
	deep := filepath.Join(root, "internal", "logic", "userservice")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, projectRoot, foundDesign, err := Find(deep)
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if projectRoot != rootAbs {
		t.Errorf("project root: got %q want %q", projectRoot, rootAbs)
	}
	designAbs, _ := filepath.Abs(designDir)
	if foundDesign != designAbs {
		t.Errorf("design dir: got %q want %q", foundDesign, designAbs)
	}
}

// TestFindManifestAtRoot keeps the legacy layout working: manifest at the
// project root, design folder anywhere by convention. Project root is
// then the manifest's parent.
func TestFindManifestAtRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), "")
	_, projectRoot, designDir, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if designDir != rootAbs {
		t.Errorf("design dir: got %q want %q", designDir, rootAbs)
	}
	if projectRoot != filepath.Dir(rootAbs) {
		t.Errorf("project root: got %q want %q", projectRoot, filepath.Dir(rootAbs))
	}
}

// TestFindAtEmptyRootUsesDesignParent pins the default root: with no
// project root supplied, FindAt resolves the same one Find walks up to,
// independent of the working directory.
func TestFindAtEmptyRootUsesDesignParent(t *testing.T) {
	root := t.TempDir()
	designDir := filepath.Join(root, "design")
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(designDir, Filename), "")
	t.Chdir(t.TempDir())

	_, projectRoot, foundDesign, err := FindAt(designDir, "")
	if err != nil {
		t.Fatal(err)
	}
	rootAbs, _ := filepath.Abs(root)
	if projectRoot != rootAbs {
		t.Errorf("project root: got %q want %q", projectRoot, rootAbs)
	}
	designAbs, _ := filepath.Abs(designDir)
	if foundDesign != designAbs {
		t.Errorf("design dir: got %q want %q", foundDesign, designAbs)
	}
}

// TestFindAtExplicitRootWins keeps `-c` authoritative: an explicit root
// is used as given even when the design folder sits elsewhere.
func TestFindAtExplicitRootWins(t *testing.T) {
	dir := t.TempDir()
	designDir := filepath.Join(dir, "contracts", "design")
	codeRoot := filepath.Join(dir, "services", "api")
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(designDir, Filename), "")

	_, projectRoot, _, err := FindAt(designDir, codeRoot)
	if err != nil {
		t.Fatal(err)
	}
	codeAbs, _ := filepath.Abs(codeRoot)
	if projectRoot != codeAbs {
		t.Errorf("project root: got %q want %q", projectRoot, codeAbs)
	}
}

func TestFindNotFound(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := Find(dir); err == nil {
		t.Error("expected error when manifest is missing")
	}
}

func TestFindBadManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, Filename), "not: valid: yaml: at: all\n  bad")
	if _, _, _, err := Find(dir); err == nil {
		t.Error("expected parse error")
	}
}

func TestIsDesignFile(t *testing.T) {
	accept := []string{
		"api.craftgo", "api.cg",
		"design/users/service.craftgo", "design/users/service.cg",
		"/abs/path/x.cg", "/abs/path/x.craftgo",
	}
	for _, p := range accept {
		if !IsDesignFile(p) {
			t.Errorf("IsDesignFile(%q) = false, want true", p)
		}
	}
	reject := []string{
		"craftgo.design.yaml",              // the manifest, not a source file
		"types.go", "README.md", "service", // no/other extension
		"x.craftgo.bak", "x.cgx", "x.c", // near-misses must not match
		"",
	}
	for _, p := range reject {
		if IsDesignFile(p) {
			t.Errorf("IsDesignFile(%q) = true, want false", p)
		}
	}
}

func TestFindBadManifestInsideDesign(t *testing.T) {
	root := t.TempDir()
	designDir := filepath.Join(root, "design")
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(designDir, Filename), "not: valid: yaml: at: all\n  bad")
	if _, _, _, err := Find(root); err == nil {
		t.Error("expected parse error")
	}
}

// TestResolveModulePathAtRoot pins the simple case: go.mod sits at
// projectRoot itself; the resolved path equals the module line verbatim.
func TestResolveModulePathAtRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/foo/bar\n\ngo 1.24\n")
	got, err := ResolveModulePath(root)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/bar" {
		t.Errorf("got %q, want %q", got, "github.com/foo/bar")
	}
}

// TestResolveModulePathMonorepo pins the shared-go.mod case: a single
// go.mod at the repo root, project root inside a sub-tree. The resolved
// path appends the relative path so generated imports compile.
func TestResolveModulePathMonorepo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/foo/monorepo\n")
	projectRoot := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveModulePath(projectRoot)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/monorepo/services/api" {
		t.Errorf("got %q, want %q", got, "github.com/foo/monorepo/services/api")
	}
}

// TestResolveModulePathNoGoMod pins the missing-go.mod error path: the
// returned message MUST mention `go mod init` so users get a concrete
// fix to copy-paste.
func TestResolveModulePathNoGoMod(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveModulePath(deep)
	if err == nil {
		t.Fatal("expected error when no go.mod is found")
	}
}

// TestResolveModulePathClosestGoMod pins the multi-go.mod precedence:
// a sub-module's go.mod takes precedence over a parent's, matching
// Go's own resolution rules.
func TestResolveModulePathClosestGoMod(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module github.com/foo/parent\n")
	subModule := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(subModule, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subModule, "go.mod"), "module github.com/foo/api\n")
	got, err := ResolveModulePath(subModule)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/api" {
		t.Errorf("got %q, want %q (closest go.mod wins)", got, "github.com/foo/api")
	}
}

// TestResolveModulePathQuotedModuleLine pins the rare but legal form
// `module "github.com/foo/bar"` - go.mod accepts quoted paths.
func TestResolveModulePathQuotedModuleLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module \"github.com/foo/bar\"\n")
	got, err := ResolveModulePath(root)
	if err != nil {
		t.Fatalf("ResolveModulePath: %v", err)
	}
	if got != "github.com/foo/bar" {
		t.Errorf("got %q, want %q", got, "github.com/foo/bar")
	}
}

func TestLoadFileCaseDefaultsToSnake(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.FileCase != FileCaseSnake {
		t.Errorf("default fileCase = %q, want %q", cfg.Output.FileCase, FileCaseSnake)
	}
}

func TestLoadFileCaseAcceptsKnownAndRejectsUnknown(t *testing.T) {
	for _, v := range []string{FileCaseKebab, FileCaseSnake, FileCaseCamel} {
		dir := t.TempDir()
		path := filepath.Join(dir, Filename)
		writeFile(t, path, "output:\n  fileCase: "+v+"\n")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load(fileCase=%s): %v", v, err)
		}
		if cfg.Output.FileCase != v {
			t.Errorf("fileCase = %q, want %q", cfg.Output.FileCase, v)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	writeFile(t, path, "output:\n  fileCase: pascal\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load(fileCase=pascal) = nil error, want rejection")
	}
}

// An output path outside the project has no import-path spelling, so it
// must be rejected while the manifest is read - not left to fail as a
// malformed import at `go build`.
func TestOutputPathMustStayInsideProject(t *testing.T) {
	escapes := []struct{ name, val string }{
		{"parent", "../shared/types"},
		{"deep parent", "./a/../../shared"},
		{"bare parent", ".."},
		{"absolute", "/tmp/shared"},
	}
	for _, c := range escapes {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{Output: Output{Types: c.val}}
			if err := cfg.validate(); err == nil {
				t.Errorf("output.types %q was accepted", c.val)
			}
		})
	}
	inside := []string{"", "-", "./internal/types", "contracts/types", "./a/../b"}
	for _, val := range inside {
		cfg := &Config{Output: Output{Types: val}}
		if err := cfg.validate(); err != nil {
			t.Errorf("output.types %q was rejected: %v", val, err)
		}
	}
}

// The same rule covers the event targets, which are the paths a
// contract artifact would actually be published from.
func TestEventTargetOutMustStayInsideProject(t *testing.T) {
	cfg := &Config{Events: Events{Targets: []EventTarget{{Lang: LangGo, Out: "../contracts"}}}}
	if err := cfg.validate(); err == nil {
		t.Error("events.targets out escaping the project was accepted")
	}
}

func loadManifest(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), Filename)
	writeFile(t, path, body)
	return Load(path)
}

func TestLoadProtoDefaults(t *testing.T) {
	cfg, err := loadManifest(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.PB != "./internal/pb" || cfg.Output.GRPC != "./internal/grpc" {
		t.Errorf("pb = %q grpc = %q", cfg.Output.PB, cfg.Output.GRPC)
	}
	if cfg.Output.PBDisabled() {
		t.Error("pb enabled by default")
	}
	if len(cfg.Proto.Includes) != 0 || cfg.Proto.Plugins != (Plugins{}) {
		t.Errorf("proto = %+v", cfg.Proto)
	}
	contracts, err := loadManifest(t, "output:\n  kind: contracts\n")
	if err != nil {
		t.Fatal(err)
	}
	if contracts.Output.PB != "./gen/pb" {
		t.Errorf("contracts pb = %q", contracts.Output.PB)
	}
}

func TestLoadProtoBlock(t *testing.T) {
	cfg, err := loadManifest(t, `output:
  pb: "-"
proto:
  includes: [./third_party, ./vendor/protos]
  plugins:
    go: /opt/bin/protoc-gen-go
    goGrpc: protoc-gen-go-grpc
`)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Output.PBDisabled() {
		t.Error(`pb: "-" must disable the plugins`)
	}
	if got := cfg.Proto.Includes; len(got) != 2 || got[0] != "./third_party" {
		t.Errorf("includes = %v", got)
	}
	if cfg.Proto.Plugins.Go != "/opt/bin/protoc-gen-go" || cfg.Proto.Plugins.GoGRPC != "protoc-gen-go-grpc" {
		t.Errorf("plugins = %+v", cfg.Proto.Plugins)
	}
}

func TestProtoKeysAreValidated(t *testing.T) {
	cases := map[string]string{
		`output:` + "\n  grpc: \"-\"\n":              `output.grpc cannot be "-"`,
		`output:` + "\n  pb: ../elsewhere\n":         "output.pb",
		`output:` + "\n  grpc: ./internal/types\n":   "output.types and output.grpc both write",
		`output:` + "\n  pb: ./internal/transport\n": "output.transport and output.pb both write",
		`proto:` + "\n  includes: [../shared]\n":     "proto.includes[0]",
		`proto:` + "\n  includes: [\"\"]\n":          "proto.includes[0]: an include names a directory",
	}
	for body, want := range cases {
		_, err := loadManifest(t, body)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}
