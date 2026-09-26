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

// workspaceProject is a temp project whose go.work uses this repo, so
// `go tool` resolves the plugins the root go.mod pins.
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
	for line := range strings.SplitSeq(string(goMod), "\n") {
		if after, ok := strings.CutPrefix(line, "go "); ok {
			version = strings.TrimSpace(after)
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

// The plan names the gRPC server packages and pb code a proto set adds.
func TestPlanMatchesWhatTheRunWritesWithProtos(t *testing.T) {
	dir := workspaceProject(t)
	cfg := protoConfig()
	in := Inputs{Design: analyzeProject(t, planSrc...), Protos: protoFixture(t, cfg)}
	sel, _ := selection(nil)
	if err := emit(in, cfg, dir, sel); err != nil {
		t.Fatalf("emit: %v", err)
	}
	_, planned := plan(in, cfg, dir, sel)
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

// pbCode writes plugin output for the proto at slash path source to rel under dir.
func pbCode(t *testing.T, dir, rel, source string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	header := protodesign.PluginHeaders[0]
	if strings.HasSuffix(rel, "_grpc.pb.go") {
		header = protodesign.PluginHeaders[1]
	}
	body := header + "\n// versions:\n// \tprotoc        (unknown)\n// source: " + source + "\n\npackage x\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A design with no proto sweeps nothing under output.pb: the protoc output there is the user's.
func TestPBSweepLeavesADesignWithoutProtosAlone(t *testing.T) {
	dir := t.TempDir()
	cfg := protoConfig()
	kept := []string{
		pbCode(t, dir, "internal/pb/billing/billing.pb.go", "billing/billing.proto"),
		pbCode(t, dir, "internal/pb/billing/billing_grpc.pb.go", "billing/billing.proto"),
	}
	if err := Generate(Inputs{Design: analyzeProject(t, planSrc...)}, cfg, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is not craftgo's and must survive: %v", rel(dir, p), err)
		}
	}
}

// Under output.pb the sweep deletes only plugin output of the design's own protos, in the
// directories they write into: the code of a proto an include root holds, and whatever sits in
// another directory, survives.
func TestPBSweepTakesOnlyTheDesignsProtoCode(t *testing.T) {
	dir := workspaceProject(t)
	for name, src := range map[string]string{
		"design/greet/greet.proto": `syntax = "proto3";
package greet;
import "billing/billing.proto";
import "greet/common.proto";
service Greeter { rpc Bill(billing.Invoice) returns (greet.Note); }
`,
		"design/greet/types.proto": "syntax = \"proto3\";\npackage greet;\nmessage Local { string id = 1; }\n",
		"third_party/billing/billing.proto": `syntax = "proto3";
package billing;
option go_package = "example.com/app/internal/pb/billing";
message Invoice { string id = 1; }
`,
		"third_party/greet/common.proto": `syntax = "proto3";
package greet;
option go_package = "example.com/app/internal/pb/greet";
message Note { string text = 1; }
`,
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := protoConfig()
	cfg.Proto.Includes = []string{"third_party"}
	protos, err := protodesign.Load(context.Background(), filepath.Join(dir, "design"), designopts.ProtoOptions(cfg, dir))
	if err != nil {
		t.Fatal(err)
	}
	kept := []string{
		pbCode(t, dir, "internal/pb/billing/billing.pb.go", "billing/billing.proto"),
		pbCode(t, dir, "internal/pb/greet/common.pb.go", "greet/common.proto"),
		pbCode(t, dir, "internal/pb/greet/v1/greet.pb.go", "greet/v1/greet.proto"),
		pbCode(t, dir, "internal/pb/other/other.pb.go", "other/other.proto"),
	}
	gone := []string{
		pbCode(t, dir, "internal/pb/greet/old.pb.go", "greet/old.proto"),
		pbCode(t, dir, "internal/pb/greet/types_grpc.pb.go", "greet/types.proto"),
	}
	if err := Generate(Inputs{Design: analyzeProject(t, planSrc...), Protos: protos}, cfg, dir); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "pb", "greet", "greet_grpc.pb.go")); err != nil {
		t.Fatalf("the run wrote no pb code, so the rule is untested: %v", err)
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is not the design's pb code and must survive: %v", rel(dir, p), err)
		}
	}
	for _, p := range gone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is stale pb code of a design proto and must be swept: %v", rel(dir, p), err)
		}
	}
}

// A dropped proto service loses its server package; its logic scaffold stays, and so does its pb
// code, which a design without a proto cannot tell from protoc output of the user's.
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
	if _, err := os.Stat(filepath.Join(dir, "internal", "pb", "greet", "greet_grpc.pb.go")); err != nil {
		t.Errorf("a design without a proto must sweep nothing under output.pb: %v", err)
	}
	if _, err := os.Stat(logic); err != nil {
		t.Errorf("the logic scaffold must survive: %v", err)
	}
}
