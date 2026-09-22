package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// greetProto is the gRPC half of a design: every streaming shape once.
const greetProto = `syntax = "proto3";

package greet;

service Greeter {
  rpc SayHello(HelloRequest) returns (HelloReply);
  rpc ListHellos(HelloRequest) returns (stream HelloReply);
  rpc RecordHellos(stream HelloRequest) returns (HelloReply);
  rpc Chat(stream HelloRequest) returns (stream HelloReply);
}

message HelloRequest {
  string name = 1;
}

message HelloReply {
  string message = 1;
}
`

// protoOnlyManifest turns the documents off: a design with no route has
// no OpenAPI document worth serving.
const protoOnlyManifest = `output:
  openapi: "-"
`

// grpcProject is a fresh project whose workspace names this repo and its
// published modules, so the generated code resolves craftgo out of the
// tree and `go tool` resolves the plugins the root go.mod pins.
func grpcProject(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goVersion := goDirective(t, root)
	mustWrite(t, dir, "go.mod", "module craftgo.test/grpcapp\n\ngo "+goVersion+"\n")
	uses := []string{".", root}
	for _, m := range repoModules {
		uses = append(uses, filepath.Join(root, filepath.FromSlash(m)))
	}
	mustWrite(t, dir, "go.work", "go "+goVersion+"\n\nuse (\n\t"+strings.Join(uses, "\n\t")+"\n)\n")
	t.Setenv("GOWORK", filepath.Join(dir, "go.work"))
	t.Setenv("GOFLAGS", "")
	return dir
}

func genGRPC(t *testing.T, dir string) {
	t.Helper()
	if err := runGen([]string{"-f", filepath.Join(dir, "design"), "-c", dir}); err != nil {
		t.Fatalf("runGen: %v", err)
	}
}

// goCheck builds and vets the generated project inside its workspace.
func goCheck(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"), "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", args[0], err, out)
		}
	}
}

func mustContain(t *testing.T, dir, rel string, needles ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	for _, n := range needles {
		if !strings.Contains(string(body), n) {
			t.Errorf("%s lacks %q", rel, n)
		}
	}
	return string(body)
}

// A design of protos alone generates a gRPC service that compiles: the pb
// code, the server layer, the logic stubs, the wiring and a main.go that
// boots the gRPC listener alone.
func TestRunGenProtoOnlyProjectCompiles(t *testing.T) {
	dir := grpcProject(t)
	mustWrite(t, dir, "design/craftgo.design.yaml", protoOnlyManifest)
	mustWrite(t, dir, "design/greet/greet.proto", greetProto)
	genGRPC(t, dir)

	for _, rel := range []string{
		"internal/pb/greet/greet.pb.go",
		"internal/pb/greet/greet_grpc.pb.go",
		"internal/grpc/greeter/server.go",
		"internal/grpc/greeter/say_hello.go",
		"internal/grpc/greeter/chat.go",
		"internal/service/greeter/say_hello.go",
		"internal/service/greeter/record_hellos.go",
		"internal/wiring/wiring.go",
		"internal/wiring/grpc.go",
		"config/config.go",
		"config/config.yaml",
		"svccontext/svccontext.go",
		"main.go",
	} {
		if !exists(t, dir, filepath.FromSlash(rel)) {
			t.Errorf("missing %s", rel)
		}
	}
	if exists(t, dir, "docs") || exists(t, dir, "internal", "routes") {
		t.Error("a proto-only design has no HTTP output")
	}
	main := mustContain(t, dir, "main.go", "wiring.RegisterGRPC(", "rpc.WithStatsHandler(tel.GRPCServerHandler())", "grpcSrv.Start(cfg.GRPC.Addr)")
	if strings.Contains(main, "server.New(") || strings.Contains(main, "wiring.Register(") {
		t.Error("a proto-only main.go must not boot an HTTP listener")
	}
	mustContain(t, dir, "config/config.go", "GRPCConfig", "`yaml:\"grpc\"`", `c.GRPC.Addr = ":9000"`)
	mustContain(t, dir, "config/config.yaml", "grpc:\n  addr: \":9000\"")
	mustContain(t, dir, "internal/wiring/grpc.go", "greetpb.RegisterGreeterServer(srv, greetergrpc.NewServer(svcCtx))")
	goCheck(t, dir)

	// A second run changes nothing: the plugins and the emitters are
	// deterministic, and the scaffolds are left alone.
	before := treeOf(t, dir)
	genGRPC(t, dir)
	if got := treeOf(t, dir); !sameTree(got, before) {
		t.Errorf("a second run must change nothing:\nbefore %v\nafter  %v", keysOf(before), keysOf(got))
	}
}

// Routes and RPCs in one design boot both listeners from one main.go.
func TestRunGenMixedProjectCompiles(t *testing.T) {
	dir := grpcProject(t)
	mustWrite(t, dir, "design/craftgo.design.yaml", routesOnlyManifest)
	mustWrite(t, dir, "design/api.craftgo", routesOnlyDesign)
	mustWrite(t, dir, "design/greet/greet.proto", greetProto)
	genGRPC(t, dir)
	mustContain(t, dir, "main.go", "wiring.Register(ctx, srv, svc)", "wiring.RegisterGRPC(ctx, grpcSrv, svc)", "srv.Start(cfg.Server.Addr)", "grpcSrv.Start(cfg.GRPC.Addr)")
	if !exists(t, dir, "internal", "routes", "thing_service", "routes.go") || !exists(t, dir, "internal", "grpc", "greeter", "server.go") {
		t.Error("both halves must be generated")
	}
	goCheck(t, dir)
}

// A design folder with neither a .craftgo nor a .proto is nothing to
// generate from.
func TestRunGenRejectsAnEmptyDesign(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "go.mod", "module github.com/test/empty\n\ngo 1.24\n")
	mustWrite(t, dir, "design/craftgo.design.yaml", "")
	err := runGen([]string{"-f", filepath.Join(dir, "design"), "-c", dir})
	if err == nil || !strings.Contains(err.Error(), "no .craftgo or .proto files") {
		t.Fatalf("err = %v", err)
	}
}

// Renaming a proto service (and its file) sweeps the server package and
// the pb code of the old name; the logic stubs, being the user's, stay.
func TestRenamedProtoServiceLeavesNothingBehind(t *testing.T) {
	dir := grpcProject(t)
	mustWrite(t, dir, "design/craftgo.design.yaml", protoOnlyManifest)
	mustWrite(t, dir, "design/greet/greet.proto", greetProto)
	genGRPC(t, dir)

	if err := os.Remove(filepath.Join(dir, "design", "greet", "greet.proto")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "design/hello/hello.proto", strings.NewReplacer("package greet;", "package hello;", "service Greeter", "service Hello").Replace(greetProto))
	genGRPC(t, dir)

	for _, rel := range []string{"internal/grpc/greeter", "internal/pb/greet"} {
		if exists(t, dir, filepath.FromSlash(rel)) {
			t.Errorf("%s of the renamed service survived", rel)
		}
	}
	for _, rel := range []string{"internal/grpc/hello/server.go", "internal/pb/hello/hello_grpc.pb.go", "internal/service/greeter/say_hello.go", "internal/service/hello/say_hello.go"} {
		if !exists(t, dir, filepath.FromSlash(rel)) {
			t.Errorf("missing %s", rel)
		}
	}
	mustContain(t, dir, "internal/wiring/grpc.go", "hellopb.RegisterHelloServer(srv, hellogrpc.NewServer(svcCtx))")
	if body := mustContain(t, dir, "internal/wiring/grpc.go"); strings.Contains(body, "Greeter") {
		t.Error("the wiring still registers the old service")
	}
}
