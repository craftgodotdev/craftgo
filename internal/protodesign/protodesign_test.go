package protodesign

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	names, err := Discover("testdata")
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
	req := set.Request
	if !reflect.DeepEqual(req.FileToGenerate, set.Names) {
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
	if len(req.SourceFileDescriptors) != len(set.Names) {
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
	if set.Request.GetParameter() != "paths=source_relative" {
		t.Errorf("parameter = %q", set.Request.GetParameter())
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

func TestPBEnabledKeepsGoPackageName(t *testing.T) {
	root := write(t, map[string]string{"a/a.proto": `syntax = "proto3";
package a;
option go_package = "github.com/elsewhere/a;apb";
service A { rpc Do(R) returns (R); }
message R {}
`})
	set := load(t, root, opts())
	a := service(t, set, "a.A")
	if a.PBImport != module+"/internal/pb/a" || a.Package != "apb" {
		t.Errorf("service = %+v", *a)
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
