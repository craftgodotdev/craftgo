// Package protodesign compiles the `.proto` files under the design folder
// in-process, reads each service's and RPC's Go names from protogen, and runs
// the protobuf plugins that write the pb code.
package protodesign

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/craftgodotdev/craftgo/internal/walk"
)

// Options is what a compilation needs to know about the project.
type Options struct {
	// Module is the Go import prefix of the project root.
	Module string
	// PBDir is the plugin output directory relative to the project root. Empty
	// disables the plugins, and every design file then needs `option go_package`.
	PBDir string
	// Includes lists extra absolute import roots, after the design root.
	Includes []string
	// FileCase is `output.fileCase`, applied to service directory and RPC file names.
	FileCase string
	// PluginGo and PluginGoGRPC are the proto.plugins commands; empty runs the
	// tool go.mod pins.
	PluginGo     string
	PluginGoGRPC string
}

// pbEnabled reports whether the plugins run for this project.
func (o Options) pbEnabled() bool { return o.PBDir != "" }

// pbRel returns PBDir as a clean slash path, e.g. `internal/pb`.
func (o Options) pbRel() string { return path.Clean(filepath.ToSlash(o.PBDir)) }

// pbRoot returns the pb directory on disk under projectRoot.
func (s *Set) pbRoot(projectRoot string) string {
	return filepath.Join(projectRoot, filepath.FromSlash(s.opts.pbRel()))
}

// Set is one compiled design: every proto under the design root and the
// services they declare.
type Set struct {
	// opts is what the set was compiled with.
	opts Options
	// names lists the design files the plugins generate for, as sorted slash
	// paths relative to the design root.
	names []string
	// plugin is the protogen view of the whole graph; the design files have
	// Generate set.
	plugin *protogen.Plugin
	// request is the plugin request, deps first, as the plugins receive it.
	request *pluginpb.CodeGeneratorRequest
	// Services lists every service the design files declare, sorted by full name.
	Services []*Service
	// Warnings describe design protos that compile and generate but may fail at run time.
	Warnings []string
}

// Service is one proto service and what the scaffolds need to spell it.
type Service struct {
	// Name is the Go name protogen gives the service (`Greeter`).
	Name string
	// FullName is the proto full name (`greet.Greeter`).
	FullName string
	// Package is the Go package name of the file declaring the service, which
	// the server and logic scaffolds also declare.
	Package string
	// PBImport is the Go import path of the generated pb package.
	PBImport string
	// Dir is the service's output directory name: Name in the file case.
	Dir string
	// Methods lists the RPCs in declaration order.
	Methods []*Method
}

// Kind is the streaming shape of an RPC.
type Kind int

const (
	Unary Kind = iota
	ServerStream
	ClientStream
	Bidi
)

// String returns the kind as the generated comments spell it.
func (k Kind) String() string {
	switch k {
	case ServerStream:
		return "server-streaming"
	case ClientStream:
		return "client-streaming"
	case Bidi:
		return "bidi-streaming"
	}
	return "unary"
}

// Method is one RPC.
type Method struct {
	// FullMethod is the gRPC method name (`/greet.Greeter/SayHello`).
	FullMethod string
	// Name is the Go method name (`SayHello`).
	Name string
	// File is the RPC's file name without extension: Name in the file case.
	File string
	Kind Kind
	// In and Out are the request and response message types.
	In, Out TypeRef
	// Doc is the leading comment, one line per entry, without the `//`.
	Doc []string
}

// TypeRef is a generated message type: its Go name and where it lives.
type TypeRef struct {
	Name       string
	ImportPath string
	// Package is the Go package name of the file declaring the message, used
	// as its import alias outside the service's own pb package.
	Package string
}

// HasServices reports whether any design file declares a service.
func (s *Set) HasServices() bool { return s != nil && len(s.Services) > 0 }

// PBFiles returns the files the plugins write under projectRoot:
// `<name>.pb.go` for each design file and `<name>_grpc.pb.go` for one that
// declares a service; none when the plugins are disabled.
func (s *Set) PBFiles(projectRoot string) []string {
	if s == nil || !s.opts.pbEnabled() {
		return nil
	}
	root := s.pbRoot(projectRoot)
	var out []string
	for _, name := range s.names {
		prefix := strings.TrimSuffix(name, ".proto")
		out = append(out, filepath.Join(root, filepath.FromSlash(prefix+".pb.go")))
		if f := s.plugin.FilesByPath[name]; f != nil && len(f.Services) > 0 {
			out = append(out, filepath.Join(root, filepath.FromSlash(prefix+"_grpc.pb.go")))
		}
	}
	return out
}

// OwnedPBFiles returns the plugin code under the pb directory, the project root's own files aside,
// that design files own, present or removed: each file whose plugin header names as its source a
// proto no include root holds, at the path the plugins write that proto's code to. In a directory
// no design file writes into any more - its protos moved or deleted - a file must also name
// craftgo as its [compiler] and a proto the design no longer has, so protoc or buf output of your
// own there stays.
func (s *Set) OwnedPBFiles(projectRoot string) []string {
	if s == nil || !s.opts.pbEnabled() {
		return nil
	}
	root := s.pbRoot(projectRoot)
	designDirs := map[string]bool{}
	for _, dir := range s.designDirs() {
		designDirs[dir] = true
	}
	var out []string
	// Discovery reads no proto in a directory a design walk skips, so no pb code lands in one.
	opts := walk.Options{SkipDir: walk.SkipDesignDir, Within: projectRoot, OnError: func(string, error) error { return nil }}
	_ = walk.Files(root, opts, func(file string) error {
		rel, err := filepath.Rel(root, file)
		if err != nil || !strings.HasSuffix(file, ".pb.go") || filepath.Dir(file) == filepath.Clean(projectRoot) {
			return nil
		}
		src, byCraftgo, ok := pluginSource(file)
		if !ok || s.included(src) || !writtenFor(src, filepath.ToSlash(rel)) {
			return nil
		}
		if designDirs[path.Dir(src)] || byCraftgo && !slices.Contains(s.names, src) {
			out = append(out, file)
		}
		return nil
	})
	return out
}

// writtenFor reports whether rel, a slash path under the pb directory, is where the plugins write
// the code of the proto at slash path src.
func writtenFor(src, rel string) bool {
	stem := strings.TrimSuffix(src, ".proto")
	return rel == stem+".pb.go" || rel == stem+"_grpc.pb.go"
}

// designDirs lists the directories of the design files, as sorted slash paths relative to the
// design root.
func (s *Set) designDirs() []string {
	dirs := make([]string, 0, len(s.names))
	for _, name := range s.names {
		dirs = append(dirs, path.Dir(name))
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// included reports whether an include root holds the proto at slash path src.
func (s *Set) included(src string) bool {
	for _, root := range s.opts.Includes {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(src))); err == nil {
			return true
		}
	}
	return false
}

// pluginSource returns the proto the plugin code in file was generated from, as its header
// comment names it, and whether the header names craftgo as the [compiler]; ok is false when no
// plugin header naming a source comes before the file's code.
func pluginSource(file string) (src string, byCraftgo, ok bool) {
	f, err := os.Open(file)
	if err != nil {
		return "", false, false
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	// The header may follow a comment block protoc-gen-go copies from above the proto's syntax line.
	for {
		if !lines.Scan() {
			return "", false, false
		}
		t := lines.Text()
		if slices.Contains(PluginHeaders, t) {
			break
		}
		if t != "" && !strings.HasPrefix(t, "//") {
			return "", false, false
		}
	}
	for lines.Scan() {
		line, ok := strings.CutPrefix(lines.Text(), "// ")
		if !ok {
			break
		}
		// protoc-gen-go writes "\tprotoc <version>", protoc-gen-go-grpc "- protoc <version>".
		if f := strings.Fields(strings.TrimPrefix(line, "- ")); len(f) == 2 && f[0] == "protoc" {
			byCraftgo = f[1] == compiler
		}
		if src, ok := strings.CutPrefix(line, "source: "); ok {
			return src, byCraftgo, true
		}
		if src, ok := strings.CutSuffix(line, " is a deprecated file."); ok {
			return src, byCraftgo, true
		}
	}
	return "", false, false
}
