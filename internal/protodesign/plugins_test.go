package protodesign

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

// workspaceProject returns a fresh project whose go.work uses this repo, so
// `go tool` finds the plugins the root go.mod pins.
func workspaceProject(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
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

// repoRoot walks up to the directory holding go.work.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			real, err := filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			return real
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.work above the package")
		}
		dir = parent
	}
}

// unknownCompiler is the compiler line both plugins write when the
// request carries no compiler version.
var unknownCompiler = regexp.MustCompile(`protoc\s+\(unknown\)`)

func TestRunPluginsWritesThePredictedFiles(t *testing.T) {
	project := workspaceProject(t)
	set := load(t, "testdata", opts())
	if err := RunPlugins(set, project); err != nil {
		t.Fatal(err)
	}
	pbRoot := set.PBRoot(project)
	var written []string
	_ = filepath.WalkDir(pbRoot, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			written = append(written, path)
		}
		return nil
	})
	sort.Strings(written)
	want := set.PBFiles(project)
	sort.Strings(want)
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("written %v\nplanned %v", written, want)
	}
	for _, path := range written {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		header := PluginHeaders[0]
		if strings.HasSuffix(path, "_grpc.pb.go") {
			header = PluginHeaders[1]
		}
		if !strings.HasPrefix(string(body), header) {
			t.Errorf("%s opens with %q", path, strings.SplitN(string(body), "\n", 2)[0])
		}
		if !unknownCompiler.Match(body) {
			t.Errorf("%s carries a compiler version, which would differ between machines", path)
		}
	}
	grpc, _ := os.ReadFile(filepath.Join(pbRoot, "greet", "greet_grpc.pb.go"))
	for _, want := range []string{"package greet", "type GreeterServer interface", "type UnimplementedGreeterServer struct", "grpc.ServerStreamingServer[HelloReply]", "grpc.BidiStreamingServer[HelloRequest, HelloReply]"} {
		if !strings.Contains(string(grpc), want) {
			t.Errorf("greet_grpc.pb.go lacks %q", want)
		}
	}
	pb, _ := os.ReadFile(filepath.Join(pbRoot, "greet", "greet.pb.go"))
	if !strings.Contains(string(pb), `common "example.com/app/internal/pb/common"`) {
		t.Error("the pb code does not import its sibling package under the M mapping")
	}
}

func TestRunPluginsIsANoOpWhenDisabled(t *testing.T) {
	root := write(t, map[string]string{"a/a.proto": `syntax = "proto3";
package a;
option go_package = "github.com/acme/contracts/gen/a";
message R {}
`})
	o := opts()
	o.PBDir = ""
	set := load(t, root, o)
	project := t.TempDir()
	if err := RunPlugins(set, project); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(project); len(entries) != 0 {
		t.Errorf("wrote %v with the plugins disabled", entries)
	}
}

func TestResolvePluginNamesBothRoutesWhenUnpinned(t *testing.T) {
	project := t.TempDir()
	t.Setenv("GOWORK", "off")
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/bare\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resolvePlugin(project, Plugin{Name: "protoc-gen-go"})
	if err == nil {
		t.Fatal("an unpinned tool resolved")
	}
	for _, want := range []string{"go get -tool", "proto.plugins", "protoc-gen-go is not a tool"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err)
		}
	}
	if _, err := resolvePlugin(project, Plugin{Name: "protoc-gen-go", Command: "definitely-not-on-path-xyz"}); err == nil || !strings.Contains(err.Error(), "proto.plugins names") {
		t.Errorf("a missing command must be reported: %v", err)
	}
	argv, err := resolvePlugin(project, Plugin{Name: "protoc-gen-go", Command: filepath.Join("bin", "protoc-gen-go")})
	if err != nil || len(argv) != 1 || argv[0] != filepath.Join("bin", "protoc-gen-go") {
		t.Errorf("a path is run as given: %v %v", argv, err)
	}
}

func TestWriteResponseRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../x.pb.go", "/abs.pb.go", ""} {
		err := writeResponse(t.TempDir(), responseWith(name))
		if err == nil {
			t.Errorf("%q was written", name)
		}
	}
}

func responseWith(name string) *pluginpb.CodeGeneratorResponse {
	return &pluginpb.CodeGeneratorResponse{File: []*pluginpb.CodeGeneratorResponse_File{{Name: proto.String(name), Content: proto.String("x")}}}
}

// TestToolPathIsTheLastLine checks that lines printed before the path, such as
// `go: downloading`, never become argv[0].
func TestToolPathIsTheLastLine(t *testing.T) {
	for in, want := range map[string]string{
		"/cache/protoc-gen-go\n": "/cache/protoc-gen-go",
		"go: downloading google.golang.org/protobuf v1.36.11\n/cache/x\n": "/cache/x",
		"  /cache/y  ": "/cache/y",
	} {
		if got := toolPath(in); got != want {
			t.Errorf("toolPath(%q) = %q, want %q", in, got, want)
		}
	}
}
