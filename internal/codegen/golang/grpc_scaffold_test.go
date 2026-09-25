package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The scaffolds and gRPC wiring of a proto-only and a mixed project match their goldens.
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
		mainGo, err := renderScaffold(tmpl("main.tmpl"), buildProjectMainData(tc.proj, set, cfg))
		if err != nil {
			t.Fatalf("%s: %v", tc.golden, err)
		}
		expectGolden(t, tc.golden, string(mainGo))
	}

	// A gRPC-only project configures no HTTP listener; a mixed one configures both.
	for _, shape := range []struct {
		hasHTTP bool
		suffix  string
	}{{false, "grpc"}, {true, "mixed"}} {
		data := runtimeData{
			Package:       cfg.Package,
			OperationName: operationNameFor(cfg.Package),
			ConfigImport:  goImportFromRel(cfg.Package, cfg.Output.Config),
			HasGRPC:       true,
			HasHTTP:       shape.hasHTTP,
		}
		for _, f := range []struct {
			template string
			render   func(*template.Template, any) ([]byte, error)
			golden   string
		}{
			{"config.go.tmpl", renderScaffold, "config-" + shape.suffix + ".go"},
			{"config.yaml.tmpl", execute, "config-" + shape.suffix + ".yaml"},
			{"example.config.yaml.tmpl", execute, "example-config-" + shape.suffix + ".yaml"},
		} {
			body, err := f.render(tmpl(f.template), data)
			if err != nil {
				t.Fatalf("%s: %v", f.template, err)
			}
			expectGolden(t, f.golden, string(body))
		}
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
	if pbAliasFor("greet") != "greetpb" || pbAliasFor("greetpb") != "greetpb" {
		t.Error("a package named with a pb suffix keeps it once")
	}
	imports := newImportSet(nil, goImport{}, wiringGRPCNames)
	if a, b := imports.add("greetpb", "x/a"), imports.add("greetpb", "x/b"); a != "greetpb" || b != "greetpb2" {
		t.Errorf("aliases = %s, %s", a, b)
	}
	if got := imports.add("rpc", "x/c"); got != "rpc2" {
		t.Errorf("a reserved name must be avoided, got %s", got)
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
	if err := generateRuntimeConfig(proj, nil, cfg, dir); err != nil {
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
	// The proto-era main.go keeps booting gRPC after the last proto is gone.
	if notes := scaffoldGapNotes(empty, nil, cfg, protoDir); !containsNote(notes, "still boots a gRPC listener") {
		t.Errorf("missing dropped-proto note in %v", notes)
	}
	// Fresh scaffolds match the design: nothing to say.
	fresh := t.TempDir()
	if err := generateProjectMain(proj, set, cfg, fresh); err != nil {
		t.Fatal(err)
	}
	if err := generateRuntimeConfig(proj, set, cfg, fresh); err != nil {
		t.Fatal(err)
	}
	if notes := scaffoldGapNotes(proj, set, cfg, fresh); len(notes) != 0 {
		t.Errorf("fresh scaffolds noted: %v", notes)
	}
	// Plugins disabled and no pb code generated.
	external := *cfg
	external.Output.PB = "-"
	if _, err := protodesign.Load(t.Context(), filepath.Join("testdata", "proto"), designopts.ProtoOptions(&external, ".")); err == nil {
		t.Fatal("the fixture has no go_package, so a disabled load must fail")
	}
	if notes := scaffoldGapNotes(proj, set, &external, fresh); !containsNote(notes, "holds no .pb.go yet") {
		t.Errorf("missing pb note in %v", notes)
	}
}

// The stubs of a service the design dropped stay, and the run says where
// they are - for a proto service and an HTTP one alike.
func TestOrphanedStubsAreNamed(t *testing.T) {
	cfg := scaffoldConfig(t)
	set := loadProtos(t, cfg)
	proj := analyzeProject(t, httpScaffoldSrc)
	dir := t.TempDir()
	if err := generateGRPCServices(set, cfg, dir); err != nil {
		t.Fatal(err)
	}
	if err := generateService(proj.Packages["todos"], cfg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if notes := orphanedStubNotes(proj, set, cfg, dir); len(notes) != 0 {
		t.Errorf("declared services noted: %v", notes)
	}
	notes := orphanedStubNotes(proj, nil, cfg, dir)
	if len(notes) != 1 || !strings.Contains(notes[0], "./internal/service/greeter holds logic stubs of a service the design no longer declares") {
		t.Errorf("dropped proto service: %v", notes)
	}
	empty, _ := semantic.AnalyzeProject(nil, semantic.Options{})
	if notes := orphanedStubNotes(empty, set, cfg, dir); len(notes) != 1 || !strings.Contains(notes[0], "./internal/service/todo_service holds") {
		t.Errorf("dropped HTTP service: %v", notes)
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
