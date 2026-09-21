package codegen

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// protoFixture is the Go target's greet fixture, compiled the way the
// CLI compiles a design folder.
func protoFixture(t *testing.T, cfg *config.Config) *protodesign.Set {
	t.Helper()
	set, err := protodesign.Load(context.Background(), filepath.Join("golang", "testdata", "proto"), protodesign.Options{
		Module: cfg.Package, PBDir: cfg.Output.PB, FileCase: cfg.Output.FileCase,
	})
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

// The plan must also account for the gRPC server packages a proto set
// adds to a run.
func TestPlanMatchesWhatTheRunWritesWithProtos(t *testing.T) {
	dir := t.TempDir()
	cfg := protoConfig()
	in := Inputs{Design: analyzeProject(t, planSrc...), Protos: protoFixture(t, cfg)}
	sel, _ := selection(nil)
	if err := emit(in, cfg, dir, sel); err != nil {
		t.Fatalf("emit: %v", err)
	}
	planned := regeneratedFiles(in, cfg, dir)
	written := regeneratedUnder(t, dir)
	if !written[filepath.Join(dir, "internal", "grpc", "greeter", "server.go")] {
		t.Fatal("the fixture wrote no gRPC server package, so the rule is untested")
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

// A proto service the design drops takes its server package with it,
// while its logic scaffolds - the user's code - stay.
func TestDroppedProtoServiceLosesItsServerPackage(t *testing.T) {
	dir := t.TempDir()
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
	if _, err := os.Stat(logic); err != nil {
		t.Errorf("the logic scaffold must survive: %v", err)
	}
}
