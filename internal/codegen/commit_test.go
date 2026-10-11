package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/codegen/golang"
)

// lastRun lays out the regenerated file a previous run left in dir and
// returns its path and content.
func lastRun(t *testing.T, dir string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, "internal", "types", "shop", "types.go")
	body := golang.GeneratedHeader + "\n\npackage shop\n\n// the previous run\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, body
}

// filesUnder lists every file under dir, by slash path relative to it.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, rel(dir, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A pass that fails to render a file - here a protoc plugin that cannot run,
// after the document and the types have rendered - writes nothing.
func TestAFailedRenderLeavesTheTreeAsItWas(t *testing.T) {
	dir := t.TempDir()
	cfg := protoConfig()
	cfg.Proto.Plugins.Go = filepath.Join(dir, "no-such-plugin")
	in := Inputs{Design: analyzeProject(t, planSrc...), Protos: protoFixture(t, cfg)}
	path, body := lastRun(t, dir)
	err := Generate(in, cfg, dir)
	if err == nil || !strings.Contains(err.Error(), "no-such-plugin") {
		t.Fatalf("err = %v, want the plugin failure", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Errorf("types.go was rewritten:\n%s", got)
	}
	if got := filesUnder(t, dir); len(got) != 1 {
		t.Errorf("the failed pass wrote %v", got)
	}
}

// A pass that cannot write one of its files names it and writes none.
func TestAFailedWriteLeavesTheTreeAsItWas(t *testing.T) {
	dir := t.TempDir()
	path, body := lastRun(t, dir)
	blocked := filepath.Join(dir, "internal", "routes", "routes.go")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Generate(Inputs{Design: analyzeProject(t, planSrc...)}, planConfig(), dir)
	if err == nil || !strings.Contains(err.Error(), "cannot write "+blocked) {
		t.Fatalf("err = %v, want the blocked file named", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Errorf("types.go was rewritten:\n%s", got)
	}
	if got := filesUnder(t, dir); len(got) != 1 {
		t.Errorf("the failed pass wrote %v", got)
	}
}
