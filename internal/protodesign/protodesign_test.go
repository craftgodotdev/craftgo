package protodesign

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const module = "example.com/app"

func opts() Options {
	return Options{Module: module, PBDir: "./internal/pb", FileCase: "snake"}
}

func load(t *testing.T, root string, o Options) *Set {
	t.Helper()
	set, err := Load(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if set == nil {
		t.Fatal("no set")
	}
	return set
}

// write lays out a design in a temp dir: name → source, slash paths.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, src := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func service(t *testing.T, set *Set, full string) *Service {
	t.Helper()
	for _, s := range set.Services {
		if s.FullName == full {
			return s
		}
	}
	t.Fatalf("service %s not found", full)
	return nil
}

func method(t *testing.T, svc *Service, name string) *Method {
	t.Helper()
	for _, m := range svc.Methods {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("method %s not found", name)
	return nil
}

func TestDiscoverIsSortedAndSlashRelative(t *testing.T) {
	names, err := discover("testdata")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"common/money.proto", "editions/hi.proto", "greet/greet.proto", "greet/types.proto", "root.proto"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestLoadReturnsNilWithoutProtos(t *testing.T) {
	set, err := Load(context.Background(), t.TempDir(), opts())
	if err != nil || set != nil {
		t.Fatalf("set = %v, err = %v", set, err)
	}
}

func TestLoadServices(t *testing.T) {
	set := load(t, "testdata", opts())
	var names []string
	for _, s := range set.Services {
		names = append(names, s.FullName)
	}
	if want := []string{"greet.Greeter", "hi.Hi", "root.Rooter"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("services = %v, want %v", names, want)
	}
	g := service(t, set, "greet.Greeter")
	if g.Name != "Greeter" || g.Package != "greet" || g.Dir != "greeter" || g.PBImport != module+"/internal/pb/greet" {
		t.Errorf("greeter = %+v", *g)
	}
	kinds := map[string]Kind{}
	for _, m := range g.Methods {
		kinds[m.Name] = m.Kind
	}
	want := map[string]Kind{"SayHello": Unary, "ListHellos": ServerStream, "RecordHellos": ClientStream, "Chat": Bidi, "Ping": Unary, "Quote": Unary}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v", kinds)
	}
	say := method(t, g, "SayHello")
	if say.File != "say_hello" {
		t.Errorf("file = %q", say.File)
	}
	if want := (TypeRef{Name: "HelloRequest", ImportPath: module + "/internal/pb/greet", Package: "greet"}); say.In != want {
		t.Errorf("in = %+v", say.In)
	}
	if wantDoc := []string{"SayHello answers one greeting.", "", "The second paragraph survives too."}; !reflect.DeepEqual(say.Doc, wantDoc) {
		t.Errorf("doc = %q", say.Doc)
	}
	if list := method(t, g, "ListHellos"); len(list.Doc) != 0 {
		t.Errorf("doc = %q", list.Doc)
	}
	ping := method(t, g, "Ping")
	if want := (TypeRef{Name: "Empty", ImportPath: "google.golang.org/protobuf/types/known/emptypb", Package: "emptypb"}); ping.In != want {
		t.Errorf("ping in = %+v", ping.In)
	}
	quote := method(t, g, "Quote")
	if want := (TypeRef{Name: "Money", ImportPath: module + "/internal/pb/common", Package: "common"}); quote.In != want {
		t.Errorf("quote in = %+v", quote.In)
	}
	if want := (TypeRef{Name: "Receipt", ImportPath: module + "/internal/pb/greet", Package: "greet"}); quote.Out != want {
		t.Errorf("quote out = %+v", quote.Out)
	}
	r := service(t, set, "root.Rooter")
	if r.PBImport != module+"/internal/pb" || r.Package != "pb" {
		t.Errorf("rooter = %+v", *r)
	}
	if h := service(t, set, "hi.Hi"); h.PBImport != module+"/internal/pb/editions" {
		t.Errorf("editions service = %+v", *h)
	}
}

func TestFileCaseNamesDirsAndFiles(t *testing.T) {
	o := opts()
	o.FileCase = "kebab"
	set := load(t, "testdata", o)
	g := service(t, set, "greet.Greeter")
	if g.Dir != "greeter" || method(t, g, "SayHello").File != "say-hello" {
		t.Errorf("dir = %q file = %q", g.Dir, method(t, g, "SayHello").File)
	}
}

func TestRequestIsTopologicalAndMapped(t *testing.T) {
	set := load(t, "testdata", opts())
	req := set.request
	if !reflect.DeepEqual(req.FileToGenerate, set.names) {
		t.Errorf("file_to_generate = %v", req.FileToGenerate)
	}
	if req.CompilerVersion != nil {
		t.Error("compiler_version must stay unset")
	}
	wantParam := "paths=source_relative" +
		",Mcommon/money.proto=" + module + "/internal/pb/common" +
		",Meditions/hi.proto=" + module + "/internal/pb/editions" +
		",Mgreet/greet.proto=" + module + "/internal/pb/greet" +
		",Mgreet/types.proto=" + module + "/internal/pb/greet" +
		",Mroot.proto=" + module + "/internal/pb"
	if req.GetParameter() != wantParam {
		t.Errorf("parameter = %q\nwant %q", req.GetParameter(), wantParam)
	}
	index := map[string]int{}
	for i, f := range req.ProtoFile {
		if _, dup := index[f.GetName()]; dup {
			t.Errorf("%s listed twice", f.GetName())
		}
		index[f.GetName()] = i
	}
	for dep, user := range map[string]string{
		"common/money.proto":          "greet/greet.proto",
		"google/protobuf/empty.proto": "greet/greet.proto",
	} {
		if index[dep] >= index[user] {
			t.Errorf("%s must precede %s: %v", dep, user, index)
		}
	}
	if len(req.SourceFileDescriptors) != len(set.names) {
		t.Errorf("source_file_descriptors = %d", len(req.SourceFileDescriptors))
	}
	if req.ProtoFile[index["greet/greet.proto"]].SourceCodeInfo == nil {
		t.Error("source info dropped: the plugins would lose the comments")
	}
}

func TestPBFiles(t *testing.T) {
	set := load(t, "testdata", opts())
	want := []string{
		"internal/pb/common/money.pb.go",
		"internal/pb/editions/hi.pb.go", "internal/pb/editions/hi_grpc.pb.go",
		"internal/pb/greet/greet.pb.go", "internal/pb/greet/greet_grpc.pb.go",
		"internal/pb/greet/types.pb.go",
		"internal/pb/root.pb.go", "internal/pb/root_grpc.pb.go",
	}
	for i, w := range want {
		want[i] = filepath.FromSlash(w)
	}
	if got := set.PBFiles(""); !reflect.DeepEqual(got, want) {
		t.Errorf("pb files = %v\nwant %v", got, want)
	}
}

// The design owns plugin code in its protos' directories whose source proto no include root
// holds, whether the proto is still there or not.
func TestOwnedPBFiles(t *testing.T) {
	shared := write(t, map[string]string{"a/shared.proto": "syntax = \"proto3\";\npackage a;\noption go_package = \"example.com/app/internal/pb/a\";\nmessage S {}\n"})
	root := write(t, map[string]string{
		"a/a.proto":  "syntax = \"proto3\";\npackage a;\nmessage R {}\n",
		"root.proto": "syntax = \"proto3\";\npackage root;\nmessage Q {}\n",
	})
	o := opts()
	o.Includes = []string{shared}
	set := load(t, root, o)
	project := t.TempDir()
	pb := func(rel, head string) string {
		p := filepath.Join(project, "internal", "pb", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(head+"\n\npackage x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	goHeader := PluginHeaders[0] + "\n// versions:\n// \tprotoc-gen-go v1\n// \tprotoc        (unknown)\n"
	grpcHeader := PluginHeaders[1] + "\n// versions:\n// - protoc-gen-go-grpc v1\n// - protoc             (unknown)\n"
	want := []string{
		pb("a/a.pb.go", goHeader+"// source: a/a.proto"),
		pb("a/gone_grpc.pb.go", grpcHeader+"// source: a/gone.proto"),
		pb("a/old.pb.go", goHeader+"// a/old.proto is a deprecated file."),
		pb("root.pb.go", goHeader+"// source: root.proto"),
	}
	pb("a/shared.pb.go", goHeader+"// source: a/shared.proto")
	pb("a/moved.pb.go", goHeader+"// source: b/moved.proto")
	pb("a/hand.go", "// Code generated by hand. DO NOT EDIT.\n// source: a/hand.proto")
	pb("a/nosource.pb.go", goHeader)
	pb("a/v1/a.pb.go", goHeader+"// source: a/v1/a.proto")
	pb("b/b.pb.go", goHeader+"// source: b/b.proto")

	got := set.OwnedPBFiles(project)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("owned = %v\nwant %v", got, want)
	}
	o.PBDir = ""
	o.Includes = nil
	root = write(t, map[string]string{"a/a.proto": "syntax = \"proto3\";\npackage a;\noption go_package = \"example.com/app/internal/pb/a\";\nmessage R {}\n"})
	if got := load(t, root, o).OwnedPBFiles(project); got != nil {
		t.Errorf("no plugin, but OwnedPBFiles = %v", got)
	}
}

// With the pb directory at the project root, the root is never scanned; a design directory
// below it is.
func TestOwnedPBFilesSkipTheProjectRoot(t *testing.T) {
	root := write(t, map[string]string{
		"a/a.proto":  "syntax = \"proto3\";\npackage a;\nmessage R {}\n",
		"root.proto": "syntax = \"proto3\";\npackage root;\nmessage Q {}\n",
	})
	project := write(t, map[string]string{
		"billing.pb.go": PluginHeaders[0] + "\n// source: billing.proto\n\npackage root\n",
		"a/a.pb.go":     PluginHeaders[0] + "\n// source: a/a.proto\n\npackage a\n",
	})
	o := opts()
	o.PBDir = "."
	if got, want := load(t, root, o).OwnedPBFiles(project), []string{filepath.Join(project, "a", "a.pb.go")}; !reflect.DeepEqual(got, want) {
		t.Errorf("owned = %v, want %v", got, want)
	}
}

func TestPBDisabledHonoursGoPackage(t *testing.T) {
	root := write(t, map[string]string{"a/a.proto": `syntax = "proto3";
package a;
option go_package = "github.com/acme/contracts/gen/a;apb";
service A { rpc Do(R) returns (R); }
message R {}
`})
	o := opts()
	o.PBDir = ""
	set := load(t, root, o)
	if set.request.GetParameter() != "paths=source_relative" {
		t.Errorf("parameter = %q", set.request.GetParameter())
	}
	a := service(t, set, "a.A")
	if a.PBImport != "github.com/acme/contracts/gen/a" || a.Package != "apb" {
		t.Errorf("service = %+v", *a)
	}
	if got := set.PBFiles(""); got != nil {
		t.Errorf("no plugin, but PBFiles = %v", got)
	}
}

func TestPBDisabledRequiresGoPackage(t *testing.T) {
	root := write(t, map[string]string{"a/a.proto": `syntax = "proto3";
package a;
message R {}
`})
	o := opts()
	o.PBDir = ""
	_, err := Load(context.Background(), root, o)
	if err == nil || !strings.Contains(err.Error(), "go_package") {
		t.Fatalf("err = %v", err)
	}
}

// A Go package name clash is reported between the files that make it, the same on every run, and
// a missing go_package is pinned on the proto that lacks it.
func TestGoPackageProblemsNameTheirFiles(t *testing.T) {
	clash := write(t, map[string]string{
		"x/a.proto": "syntax = \"proto3\";\npackage x;\noption go_package = \";alpha\";\nmessage A {}\nservice S { rpc Do(A) returns (A); }\n",
		"x/b.proto": "syntax = \"proto3\";\npackage x;\nmessage B {}\n",
	})
	first := ""
	for range 5 {
		_, err := Load(context.Background(), clash, opts())
		if err == nil {
			t.Fatal("a clash was accepted")
		}
		if first == "" {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("the report changes between runs:\n%s\n%s", first, err)
		}
	}
	for _, want := range []string{`x/a.proto names its Go package example.com/app/internal/pb/x "alpha" but x/b.proto names it "x"`, "the protos of one directory are one Go package"} {
		if !strings.Contains(first, want) {
			t.Errorf("missing %q in:\n%s", want, first)
		}
	}
	if strings.Contains(first, "proto.includes") {
		t.Errorf("a design without includes is pointed at them:\n%s", first)
	}

	shared := write(t, map[string]string{"shared/id.proto": "syntax = \"proto3\";\npackage shared;\nmessage ID {}\n"})
	root := write(t, map[string]string{"a/a.proto": "syntax = \"proto3\";\npackage a;\nimport \"shared/id.proto\";\nservice A { rpc Get(shared.ID) returns (shared.ID); }\n"})
	o := opts()
	o.Includes = []string{shared}
	_, err := Load(context.Background(), root, o)
	if err == nil || !strings.Contains(err.Error(), "shared/id.proto, read from proto.includes, has no `option go_package`") {
		t.Errorf("err = %v", err)
	}
}

// With the plugins on, a go_package inside the module names the package and its path is ignored.
func TestPBEnabledKeepsGoPackageName(t *testing.T) {
	for _, goPackage := range []string{module + "/gen/a;apb", ";apb", "apb;apb", "./a;apb"} {
		root := write(t, map[string]string{"a/a.proto": `syntax = "proto3";
package a;
option go_package = "` + goPackage + `";
service A { rpc Do(R) returns (R); }
message R {}
`})
		set := load(t, root, opts())
		a := service(t, set, "a.A")
		if a.PBImport != module+"/internal/pb/a" || a.Package != "apb" {
			t.Errorf("go_package %q: service = %+v", goPackage, *a)
		}
	}
}

// A third-party proto in the design folder is refused while the plugins run: its generated copy
// would register the file a second time and panic at startup. With output.pb "-" a go_package
// elsewhere is the design's own pipeline's.
func TestThirdPartyProtosInTheDesignAreRefused(t *testing.T) {
	root := write(t, map[string]string{
		"google/rpc/status.proto": `syntax = "proto3";
package google.rpc;
option go_package = "google.golang.org/genproto/googleapis/rpc/status;status";
message Status { int32 code = 1; }
`,
		"google/protobuf/empty.proto": "syntax = \"proto3\";\npackage google.protobuf;\noption go_package = \"google.golang.org/protobuf/types/known/emptypb\";\nmessage Empty {}\n",
		"vendor/v.proto":              "syntax = \"proto3\";\npackage v;\noption go_package = \"github.com/elsewhere/v;v\";\nmessage V {}\n",
		"api/api.proto": `syntax = "proto3";
package api;
import "google/rpc/status.proto";
import "google/protobuf/empty.proto";
import "vendor/v.proto";
message Job { google.rpc.Status error = 1; v.V v = 2; }
service Jobs { rpc Get(Job) returns (google.protobuf.Empty); }
`,
	})
	_, err := Load(context.Background(), root, opts())
	if err == nil {
		t.Fatal("third-party protos were accepted")
	}
	for _, want := range []string{
		"google/rpc/status.proto is a proto whose Go code the craftgo runtime links in already",
		"google/protobuf/empty.proto copies a well-known type",
		"vendor/v.proto has go_package github.com/elsewhere/v, outside module example.com/app",
		"move it under a proto.includes root",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "api/api.proto") {
		t.Errorf("the design's own proto is refused:\n%s", err)
	}

	o := opts()
	o.PBDir = ""
	foreign := write(t, map[string]string{"vendor/v.proto": "syntax = \"proto3\";\npackage v;\noption go_package = \"github.com/elsewhere/v;v\";\nmessage V {}\n"})
	if _, err := Load(context.Background(), foreign, o); err != nil {
		t.Errorf("with output.pb \"-\" a go_package elsewhere is refused: %v", err)
	}
}

func TestCompileErrorsCarryEveryPosition(t *testing.T) {
	root := write(t, map[string]string{"bad/bad.proto": `syntax = "proto3";
package bad;
service S {
  rpc A(Missing) returns (R);
  rpc B(R) returns (AlsoMissing);
}
message R {}
`})
	_, err := Load(context.Background(), root, opts())
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	prefix := filepath.Join(root, "bad", "bad.proto")
	for _, want := range []string{prefix + ":4:9:", prefix + ":5:21:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}

// An error in a proto read from an include root is reported at that file, not under the design.
func TestCompileErrorsInAnIncludeNameItsFile(t *testing.T) {
	shared := write(t, map[string]string{"acme/common.proto": `syntax = "proto3";
package acme;
option go_package = "example.com/acme;acme";
message Thing { strin id = 1; }
`})
	root := write(t, map[string]string{"greet/greet.proto": `syntax = "proto3";
package greet;
import "acme/common.proto";
service Greeter { rpc Do(acme.Thing) returns (acme.Thing); }
`})
	o := opts()
	o.Includes = []string{shared}
	_, err := Load(context.Background(), root, o)
	if err == nil {
		t.Fatal("no error")
	}
	if want := filepath.Join(shared, "acme", "common.proto") + ":4:17:"; !strings.Contains(err.Error(), want) {
		t.Errorf("missing %q in:\n%s", want, err)
	}
	if strings.Contains(err.Error(), filepath.Join(root, "acme")) {
		t.Errorf("the error names a file under the design root, which holds none:\n%s", err)
	}
}

// A proto under an include root inside the design folder is an include, read once by its path
// below that root, and not a design proto.
func TestIncludeRootInsideTheDesignIsNotTheDesign(t *testing.T) {
	root := write(t, map[string]string{
		"third_party/acme/common.proto": `syntax = "proto3";
package acme;
option go_package = "example.com/acme;acme";
message Thing { string id = 1; }
`,
		"greet/greet.proto": `syntax = "proto3";
package greet;
import "acme/common.proto";
service Greeter { rpc Do(acme.Thing) returns (acme.Thing); }
`,
	})
	o := opts()
	o.Includes = []string{filepath.Join(root, "third_party")}
	set := load(t, root, o)
	if !reflect.DeepEqual(set.names, []string{"greet/greet.proto"}) {
		t.Errorf("design protos = %v", set.names)
	}
	do := method(t, service(t, set, "greet.Greeter"), "Do")
	if want := (TypeRef{Name: "Thing", ImportPath: "example.com/acme", Package: "acme"}); do.In != want {
		t.Errorf("in = %+v", do.In)
	}
	// The design root itself as an include root, or one beside it, keeps every design proto.
	names := []string{"greet/greet.proto", "third_party/acme/common.proto"}
	for _, inc := range []string{root, filepath.Join(root, "..", "third_party")} {
		if got := withoutIncluded(root, names, []string{inc}); !reflect.DeepEqual(got, names) {
			t.Errorf("include root %s: design protos = %v", inc, got)
		}
	}
}

func TestIncludesResolveImports(t *testing.T) {
	shared := write(t, map[string]string{"shared/id.proto": `syntax = "proto3";
package shared;
option go_package = "github.com/acme/shared/gen/shared";
message ID { string value = 1; }
`})
	root := write(t, map[string]string{"a/a.proto": `syntax = "proto3";
package a;
import "shared/id.proto";
service A { rpc Get(shared.ID) returns (shared.ID); }
`})
	o := opts()
	o.Includes = []string{shared}
	set := load(t, root, o)
	get := method(t, service(t, set, "a.A"), "Get")
	if want := (TypeRef{Name: "ID", ImportPath: "github.com/acme/shared/gen/shared", Package: "shared"}); get.In != want {
		t.Errorf("in = %+v", get.In)
	}
	if got := set.PBFiles("proj"); !reflect.DeepEqual(got, []string{filepath.Join("proj", "internal", "pb", "a", "a.pb.go"), filepath.Join("proj", "internal", "pb", "a", "a_grpc.pb.go")}) {
		t.Errorf("pb files = %v", got)
	}
}

func TestCollisions(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"two packages in one directory": {
			files: map[string]string{
				"x/a.proto": "syntax = \"proto3\";\npackage a;\nmessage A {}\n",
				"x/b.proto": "syntax = \"proto3\";\npackage b;\nmessage B {}\n",
			},
			want: "one directory, one package",
		},
		"rpc names sharing a file": {
			files: map[string]string{"x/a.proto": `syntax = "proto3";
package a;
service S { rpc GetUser(R) returns (R); rpc Get_User(R) returns (R); }
message R {}
`},
			want: `both generate file "get_user.go"`,
		},
		"service names sharing a directory": {
			files: map[string]string{"x/a.proto": `syntax = "proto3";
package a;
service UserService { rpc A(R) returns (R); }
service User_Service { rpc A(R) returns (R); }
message R {}
`},
			want: `both generate into directory "user_service"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(context.Background(), write(t, tc.files), opts())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// Protos that import one way but whose directories import each other are refused, naming the
// packages and the imports that close the ring; directories that import one way pass.
func TestDirectoriesImportingInARingAreRefused(t *testing.T) {
	files := map[string]string{
		"a/a1.proto": "syntax = \"proto3\";\npackage a;\nmessage A1 { string x = 1; }\n",
		"b/b1.proto": "syntax = \"proto3\";\npackage b;\nimport \"a/a1.proto\";\nmessage B1 { a.A1 a = 1; }\n",
		"a/a2.proto": "syntax = \"proto3\";\npackage a;\nimport \"a/a1.proto\";\nimport \"b/b1.proto\";\nmessage A2 { b.B1 b = 1; }\nservice S { rpc Do(A2) returns (A1); }\n",
	}
	_, err := Load(context.Background(), write(t, files), opts())
	if err == nil {
		t.Fatal("an import ring between directories was accepted")
	}
	want := "Go packages example.com/app/internal/pb/a and example.com/app/internal/pb/b import one another, which Go refuses: " +
		"a/a2.proto imports b/b1.proto and b/b1.proto imports a/a1.proto - each proto directory is one Go package"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("missing %q in:\n%s", want, err)
	}

	// Three directories in a ring are named in order.
	ring := map[string]string{
		"a/a.proto": "syntax = \"proto3\";\npackage a;\nimport \"b/b.proto\";\nmessage A { b.B b = 1; }\n",
		"b/b.proto": "syntax = \"proto3\";\npackage b;\nimport \"c/c.proto\";\nmessage B { c.C c = 1; }\n",
		"c/c.proto": "syntax = \"proto3\";\npackage c;\nimport \"a/x.proto\";\nmessage C { a.X x = 1; }\n",
		"a/x.proto": "syntax = \"proto3\";\npackage a;\nmessage X {}\n",
	}
	_, err = Load(context.Background(), write(t, ring), opts())
	if err == nil || !strings.Contains(err.Error(), "a/a.proto imports b/b.proto, b/b.proto imports c/c.proto and c/c.proto imports a/x.proto") {
		t.Errorf("err = %v", err)
	}

	delete(files, "a/a2.proto")
	files["c/c2.proto"] = "syntax = \"proto3\";\npackage c;\nimport \"b/b1.proto\";\nmessage C2 { b.B1 b = 1; }\nservice S { rpc Do(C2) returns (C2); }\n"
	load(t, write(t, files), opts())
}

// A plugin file's source is read past the comment block protoc-gen-go copies from above the
// proto's `syntax` line, such as a license header; a file whose first code comes before the
// plugin header is not a plugin file.
func TestPluginSourceReadsPastALeadingComment(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		body, want string
		ok         bool
	}{
		"plain":   {PluginHeaders[0] + "\n// versions:\n// source: greet/greet.proto\n\npackage greet\n", "greet/greet.proto", true},
		"license": {"// Copyright 2026 Example.\n// SPDX-License-Identifier: MIT\n\n" + PluginHeaders[1] + "\n// source: greet/greet.proto\n\npackage greet\n", "greet/greet.proto", true},
		"code":    {"package greet\n\n" + PluginHeaders[0] + "\n// source: greet/greet.proto\n", "", false},
	} {
		path := filepath.Join(dir, name+".pb.go")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := pluginSource(path); got != c.want || ok != c.ok {
			t.Errorf("%s: pluginSource = %q, %v; want %q, %v", name, got, ok, c.want, c.ok)
		}
	}
}
