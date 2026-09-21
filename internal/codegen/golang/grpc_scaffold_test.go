package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The gen-once scaffolds of a project with gRPC services, pinned like the
// HTTP-only ones: a proto-only project boots the gRPC listener alone, a
// mixed one boots both under one signal wait.
func TestGRPCScaffoldsArePinned(t *testing.T) {
	cfg := scaffoldConfig(t)
	set := loadProtos(t, cfg)
	empty, _ := semantic.AnalyzeProject(nil, semantic.Options{})

	for _, tc := range []struct {
		golden string
		proj   *semantic.Project
	}{
		{"main-grpc.go", empty},
		{"main-mixed.go", analyzeProject(t, httpScaffoldSrc)},
	} {
		mainGo, err := renderGo(tmpl("main.tmpl"), buildProjectMainData(tc.proj, set, cfg))
		if err != nil {
			t.Fatalf("%s: %v", tc.golden, err)
		}
		expectGolden(t, tc.golden, string(mainGo))
	}

	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  goImportFromRel(cfg.Package, cfg.Output.Config),
		HasGRPC:       true,
	}
	for _, f := range []struct {
		template string
		formatGo bool
		golden   string
	}{
		{"config.go.tmpl", true, "config-grpc.go"},
		{"config.yaml.tmpl", false, "config-grpc.yaml"},
		{"example.config.yaml.tmpl", false, "example-config-grpc.yaml"},
	} {
		body, err := renderRuntimeTemplate(f.template, data, f.formatGo)
		if err != nil {
			t.Fatalf("%s: %v", f.template, err)
		}
		expectGolden(t, f.golden, string(body))
	}

	wiring, err := renderGo(tmpl("wiring_grpc.tmpl"), buildWiringGRPCData(set, cfg))
	if err != nil {
		t.Fatal(err)
	}
	expectGolden(t, "wiring-grpc.go", string(wiring))
}

// A project whose design holds RPCs but no route still gets a main.go,
// and one with neither gets none.
func TestMainIsWrittenForProtoOnlyProjects(t *testing.T) {
	cfg := scaffoldConfig(t)
	set := loadProtos(t, cfg)
	empty, _ := semantic.AnalyzeProject(nil, semantic.Options{})
	dir := t.TempDir()
	if err := generateProjectMain(empty, set, cfg, dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("proto-only main.go not written: %v", err)
	}
	if strings.Contains(string(body), "server.New(") || !strings.Contains(string(body), "wiring.RegisterGRPC(") {
		t.Error("a proto-only main.go boots the gRPC listener alone")
	}
	none := t.TempDir()
	if err := generateProjectMain(empty, nil, cfg, none); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(none, "main.go")); !os.IsNotExist(err) {
		t.Error("a design with nothing to boot must get no main.go")
	}
}

// wiring/grpc.go exists only while a proto declares a service, and the
// aliases it uses stay distinct when two services share a package name.
func TestWiringGRPCIsWrittenOnlyWithServices(t *testing.T) {
	cfg := scaffoldConfig(t)
	dir := t.TempDir()
	if err := generateWiringGRPC(nil, cfg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "wiring", "grpc.go")); !os.IsNotExist(err) {
		t.Error("grpc.go written without services")
	}
	set := loadProtos(t, cfg)
	if err := generateWiringGRPC(set, cfg, dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "internal", "wiring", "grpc.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "greetpb.RegisterGreeterServer(srv, greetergrpc.NewServer(svcCtx))") {
		t.Errorf("registration line missing:\n%s", body)
	}
	aliases := newAliasTable()
	if a, b := aliases.claim("greetpb", "x/a"), aliases.claim("greetpb", "x/b"); a == b {
		t.Errorf("two packages got one alias: %s", a)
	}
	if aliases.claim("greetpb", "x/a") != "greetpb" {
		t.Error("an import path must keep its first alias")
	}
}

// The notes name every gen-once scaffold that predates the transports
// the design now has.
func TestScaffoldGapNotes(t *testing.T) {
	cfg := scaffoldConfig(t)
	set := loadProtos(t, cfg)
	proj := analyzeProject(t, httpScaffoldSrc)
	dir := t.TempDir()
	// An HTTP-era main.go and config.go, then protos arrive.
	if err := generateProjectMain(proj, nil, cfg, dir); err != nil {
		t.Fatal(err)
	}
	if err := generateRuntimeConfig(nil, cfg, dir); err != nil {
		t.Fatal(err)
	}
	notes := scaffoldGapNotes(proj, set, cfg, dir)
	for _, want := range []string{"never calls wiring.RegisterGRPC", "has no GRPCConfig"} {
		if !containsNote(notes, want) {
			t.Errorf("missing note %q in %v", want, notes)
		}
	}
	if containsNote(notes, "never calls wiring.Register ") {
		t.Errorf("the HTTP call is present, yet noted: %v", notes)
	}
	// A proto-era main.go, then routes arrive.
	empty, _ := semantic.AnalyzeProject(nil, semantic.Options{})
	protoDir := t.TempDir()
	if err := generateProjectMain(empty, set, cfg, protoDir); err != nil {
		t.Fatal(err)
	}
	if notes := scaffoldGapNotes(proj, set, cfg, protoDir); !containsNote(notes, "never calls wiring.Register -") {
		t.Errorf("missing HTTP note in %v", notes)
	}
	// Fresh scaffolds match the design: nothing to say.
	fresh := t.TempDir()
	if err := generateProjectMain(proj, set, cfg, fresh); err != nil {
		t.Fatal(err)
	}
	if err := generateRuntimeConfig(set, cfg, fresh); err != nil {
		t.Fatal(err)
	}
	if notes := scaffoldGapNotes(proj, set, cfg, fresh); len(notes) != 0 {
		t.Errorf("fresh scaffolds noted: %v", notes)
	}
	// Plugins disabled and no pb code yet.
	external := *cfg
	external.Output.PB = "-"
	disabled, err := protodesign.Load(t.Context(), filepath.Join("testdata", "proto"), protodesign.Options{Module: cfg.Package, FileCase: cfg.Output.FileCase})
	if err == nil {
		t.Fatal("the fixture has no go_package, so a disabled load must fail")
	}
	_ = disabled
	if notes := scaffoldGapNotes(proj, set, &external, fresh); !containsNote(notes, "holds no .pb.go yet") {
		t.Errorf("missing pb note in %v", notes)
	}
}

func containsNote(notes []string, needle string) bool {
	for _, n := range notes {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}
