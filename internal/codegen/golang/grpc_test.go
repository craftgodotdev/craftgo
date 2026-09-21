package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// loadProtos compiles the greet fixture the way `craftgo gen` would for
// a manifest with every default.
func loadProtos(t *testing.T, cfg *config.Config) *protodesign.Set {
	t.Helper()
	set, err := protodesign.Load(context.Background(), filepath.Join("testdata", "proto"), protodesign.Options{
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

// Every RPC shape the server layer and the logic scaffold can take, one
// golden each: the four streaming kinds, a call on well-known types
// only (no pb import), and a request from another proto package.
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
