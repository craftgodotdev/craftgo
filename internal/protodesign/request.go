package protodesign

import (
	"path"
	"strings"

	"github.com/bufbuild/protocompile/linker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// buildRequest is the request protoc would hand a plugin for names:
// every file they transitively import, deps before dependents, each
// exactly once, then the design files themselves. compiler_version is
// left unset, so the pb headers read `protoc (unknown)` on every machine.
func buildRequest(files linker.Files, names []string, parameter string) *pluginpb.CodeGeneratorRequest {
	req := &pluginpb.CodeGeneratorRequest{FileToGenerate: names}
	if parameter != "" {
		req.Parameter = proto.String(parameter)
	}
	seen := map[string]bool{}
	var visit func(fd protoreflect.FileDescriptor)
	visit = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			visit(imports.Get(i).FileDescriptor)
		}
		req.ProtoFile = append(req.ProtoFile, protodesc.ToFileDescriptorProto(fd))
	}
	for _, name := range names {
		if f := files.FindFileByPath(name); f != nil {
			visit(f)
		}
	}
	// A plugin reads source-retention options for the generated files from
	// here; protoc sends the same descriptors, and so does this.
	for _, fdp := range req.ProtoFile {
		if isGenerated(fdp, names) {
			req.SourceFileDescriptors = append(req.SourceFileDescriptors, fdp)
		}
	}
	return req
}

func isGenerated(fdp *descriptorpb.FileDescriptorProto, names []string) bool {
	for _, n := range names {
		if fdp.GetName() == n {
			return true
		}
	}
	return false
}

// parameter is the plugin parameter string. `paths=source_relative` puts
// `a/b.proto` at `<out>/a/b.pb.go`, and with the plugins enabled every
// design file gets an M mapping placing its Go package under output.pb -
// the one rule for where pb code lives, whatever `go_package` says
// (protogen lets M win the path and keeps go_package's `;name`).
func parameter(names []string, opts Options) string {
	params := []string{"paths=source_relative"}
	if !opts.PBEnabled() {
		return params[0]
	}
	for _, name := range names {
		params = append(params, "M"+name+"="+pbImportPath(opts.Module, opts.PBDir, name))
	}
	return strings.Join(params, ",")
}

// pbImportPath is the Go import path of the pb package for one design
// file: the module, the pb output directory, then the file's directory.
func pbImportPath(module, pbDir, name string) string {
	return joinSlash(module, strings.TrimPrefix(pbDir, "./"), path.Dir(name))
}

// joinSlash joins path elements with `/`, dropping empty and `.` ones.
func joinSlash(elems ...string) string {
	var parts []string
	for _, e := range elems {
		e = strings.Trim(strings.ReplaceAll(e, "\\", "/"), "/")
		if e != "" && e != "." {
			parts = append(parts, e)
		}
	}
	return strings.Join(parts, "/")
}
