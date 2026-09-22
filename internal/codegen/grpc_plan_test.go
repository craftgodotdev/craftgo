package codegen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// workspaceProject is a fresh project whose go.work names this repo, so
// `go tool` resolves the plugins the root go.mod pins - the way a
// project's own `tool` directives would.
func workspaceProject(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	version := "1.26"
	for _, line := range strings.Split(string(goMod), "\n") {
		if strings.HasPrefix(line, "go ") {
			version = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	for name, body := range map[string]string{
		"go.mod":  "module example.com/app\n\ngo " + version + "\n",
		"go.work": "go " + version + "\n\nuse (\n\t.\n\t" + root + "\n)\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", filepath.Join(dir, "go.work"))
	t.Setenv("GOFLAGS", "")
	return dir
}

// protoFixture is the Go target's greet fixture, compiled the way the
// CLI compiles a design folder.
func protoFixture(t *testing.T, cfg *config.Config) *protodesign.Set {
	t.Helper()
	set, err := protodesign.Load(context.Background(), filepath.Join("golang", "testdata", "proto"), designopts.ProtoOptions(cfg, "."))
	if err != nil {
		t.Fatal(err)
	}
	if !set.HasServices() {
		t.Fatal("fixture declares no service")
	}
	return set
}

// protoConfig is the plan config with the gRPC keys at their defaults.
func protoConfig() *config.Config {
	cfg := planConfig()
	cfg.Output.PB = "./internal/pb"
	cfg.Output.GRPC = "./internal/grpc"
	return cfg
}

// The plan must also account for the gRPC server packages and the pb
// code a proto set adds to a run.
func TestPlanMatchesWhatTheRunWritesWithProtos(t *testing.T) {
	dir := workspaceProject(t)
	cfg := protoConfig()
	in := Inputs{Design: analyzeProject(t, planSrc...), Protos: protoFixture(t, cfg)}
	sel, _ := selection(nil)
	if err := emit(in, cfg, dir, sel); err != nil {
		t.Fatalf("emit: %v", err)
	}
	planned := regeneratedFiles(in, cfg, dir)
	written := regeneratedUnder(t, dir)
	for _, rel := range []string{filepath.Join("internal", "grpc", "greeter", "server.go"), filepath.Join("internal", "pb", "greet", "greet_grpc.pb.go")} {
		if !written[filepath.Join(dir, rel)] {
			t.Fatalf("the fixture wrote no %s, so the rule is untested", rel)
		}
	}
	for _, f := range sortedPaths(written) {
		if !planned[f] {
			t.Errorf("%s is regenerated but unplanned - the sweep would delete it; add it to the plan", rel(dir, f))
		}
	}
	for _, f := range sortedPaths(planned) {
		if !written[f] {
			t.Errorf("%s is planned but nothing wrote it - drop it from the plan", rel(dir, f))
		}
	}
}

// A proto service the design drops takes its server package and its pb
// code with it, while its logic scaffolds - the user's code - stay.
func TestDroppedProtoServiceLosesItsServerPackage(t *testing.T) {
	dir := workspaceProject(t)
	cfg := protoConfig()
	proj := analyzeProject(t, planSrc...)
	if err := Generate(Inputs{Design: proj, Protos: protoFixture(t, cfg)}, cfg, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	server := filepath.Join(dir, "internal", "grpc", "greeter", "say_hello.go")
	logic := filepath.Join(dir, "internal", "service", "greeter", "say_hello.go")
	for _, p := range []string{server, logic} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("first run did not write %s: %v", rel(dir, p), err)
		}
	}
	if err := Generate(Inputs{Design: proj}, cfg, dir); err != nil {
		t.Fatalf("generate without protos: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "grpc", "greeter")); !os.IsNotExist(err) {
		t.Errorf("the server package of a dropped service survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "pb", "greet")); !os.IsNotExist(err) {
		t.Errorf("the pb code of a dropped proto survived: %v", err)
	}
	// The pb root is the design's, kept bare like every other output root.
	if entries, err := os.ReadDir(filepath.Join(dir, "internal", "pb")); err != nil || len(entries) != 0 {
		t.Errorf("the pb root must stay, emptied: %v %v", entries, err)
	}
	if _, err := os.Stat(logic); err != nil {
		t.Errorf("the logic scaffold must survive: %v", err)
	}
}
