package golang

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

// loadProtos compiles the testdata/proto fixture as `craftgo gen` would for cfg.
func loadProtos(t *testing.T, cfg *config.Config) *protodesign.Set {
	t.Helper()
	set, err := protodesign.Load(context.Background(), filepath.Join("testdata", "proto"), designopts.ProtoOptions(cfg, "."))
	if err != nil {
		t.Fatal(err)
	}
	if !set.HasServices() {
		t.Fatal("fixture declares no service")
	}
	return set
}

func greeter(t *testing.T, set *protodesign.Set) *protodesign.Service {
	t.Helper()
	for _, svc := range set.Services {
		if svc.Name == "Greeter" {
			return svc
		}
	}
	t.Fatal("no Greeter")
	return nil
}

// The server layer and each RPC's method and logic scaffold match their goldens.
func TestGRPCLayersArePinned(t *testing.T) {
	cfg := scaffoldConfig(t)
	svc := greeter(t, loadProtos(t, cfg))
	imps := grpcImportsFor(cfg, svc)
	server, err := renderGo(tmpl("grpc_server.tmpl"), buildGRPCServerData(svc, imps))
	if err != nil {
		t.Fatal(err)
	}
	expectGolden(t, "grpc-server.go", string(server))
	for _, m := range svc.Methods {
		method, err := renderGo(tmpl("grpc_method.tmpl"), buildGRPCMethodData(svc, m, imps))
		if err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
		expectGolden(t, "grpc-"+m.File+".go", string(method))
		logic, err := renderGo(tmpl("service.tmpl"), buildGRPCServiceData(svc, m, imps))
		if err != nil {
			t.Fatalf("%s logic: %v", m.Name, err)
		}
		expectGolden(t, "grpc-service-"+m.File+".go", string(logic))
	}
}

// The server package is regenerated; the logic scaffolds are the user's
// once written.
func TestGRPCLogicIsWrittenOnceAndServersAlways(t *testing.T) {
	cfg := scaffoldConfig(t)
	set := loadProtos(t, cfg)
	svc := greeter(t, set)
	root := t.TempDir()
	for _, gen := range []func(*protodesign.Set, *config.Config, string) error{generateGRPCServers, generateGRPCServices} {
		if err := gen(set, cfg, root); err != nil {
			t.Fatal(err)
		}
	}
	logic := filepath.Join(grpcServiceDir(root, cfg, svc), "say_hello.go")
	server := filepath.Join(grpcServerDir(root, cfg, svc), "say_hello.go")
	for _, p := range []string{logic, server} {
		if err := os.WriteFile(p, []byte("package greet // edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, gen := range []func(*protodesign.Set, *config.Config, string) error{generateGRPCServers, generateGRPCServices} {
		if err := gen(set, cfg, root); err != nil {
			t.Fatal(err)
		}
	}
	if body, _ := os.ReadFile(logic); !strings.Contains(string(body), "// edited") {
		t.Error("the logic scaffold was overwritten")
	}
	if body, _ := os.ReadFile(server); strings.Contains(string(body), "// edited") {
		t.Error("the server layer was not regenerated")
	}
	if _, err := os.Stat(filepath.Join(grpcServerDir(root, cfg, svc), "server.go")); err != nil {
		t.Error("server.go missing")
	}
}

// protoSet compiles a throwaway design holding one proto.
func protoSet(t *testing.T, cfg *config.Config, proto string) *protodesign.Set {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x", "x.proto"), []byte(proto), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := protodesign.Load(context.Background(), root, designopts.ProtoOptions(cfg, root))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// An RPC whose file or logic type collides with another generated name is rejected.
func TestValidateProtoOutputsRejectsCollidingRPCNames(t *testing.T) {
	cfg := scaffoldConfig(t)
	for proto, want := range map[string]string{
		"syntax = \"proto3\";\npackage x;\nservice S { rpc Server(R) returns (R); }\nmessage R {}\n":                         "rpc Server would generate file server.go",
		"syntax = \"proto3\";\npackage x;\nservice S { rpc Get(R) returns (R); rpc NewGet(R) returns (R); }\nmessage R {}\n": "generate a logic constructor and a logic type of one name, NewGetService",
	} {
		err := ValidateProtoOutputs(nil, protoSet(t, cfg, proto), cfg)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	ok := protoSet(t, cfg, "syntax = \"proto3\";\npackage x;\nservice S { rpc Get(R) returns (R); rpc NewServer(R) returns (R); }\nmessage R {}\n")
	if err := ValidateProtoOutputs(nil, ok, cfg); err != nil {
		t.Errorf("distinct names rejected: %v", err)
	}
}

// A proto service and a DSL service that would share one logic directory
// are rejected before anything is written.
func TestValidateProtoOutputsRejectsASharedServiceDirectory(t *testing.T) {
	cfg := scaffoldConfig(t)
	set := loadProtos(t, cfg)
	clash := analyzeProject(t, "package hello\ntype Hi { id string }\nservice Greeter {\n\tget Hi /hi { response Hi }\n}")
	err := ValidateProtoOutputs(clash, set, cfg)
	if err == nil || !strings.Contains(err.Error(), "greet.Greeter and service Greeter both generate into ./internal/service/greeter") {
		t.Fatalf("err = %v", err)
	}
	other := analyzeProject(t, "package hello\ntype Hi { id string }\nservice Hello {\n\tget Hi /hi { response Hi }\n}")
	if err := ValidateProtoOutputs(other, set, cfg); err != nil {
		t.Fatalf("distinct names rejected: %v", err)
	}
	if err := ValidateProtoOutputs(other, nil, cfg); err != nil {
		t.Fatalf("no protos rejected: %v", err)
	}
}
