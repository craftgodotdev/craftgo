package protodesign

import (
	"path"
	"slices"
	"strings"

	"github.com/bufbuild/protocompile/linker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/pluginpb"
)

// compiler is the compiler version the plugins print in every pb header,
// `protoc v0.0.0-craftgo`: the same on every machine, and what tells the code
// craftgo had the plugins write from protoc's or buf's.
const compiler = "v0.0.0-craftgo"

// buildRequest returns the request protoc would hand a plugin for names, with
// every imported file once, deps first, naming craftgo as the [compiler].
func buildRequest(files linker.Files, names []string, parameter string) *pluginpb.CodeGeneratorRequest {
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate:  names,
		CompilerVersion: &pluginpb.Version{Major: proto.Int32(0), Minor: proto.Int32(0), Patch: proto.Int32(0), Suffix: proto.String("craftgo")},
	}
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
	// Plugins read the generated files' source-retention options from
	// SourceFileDescriptors.
	for _, fdp := range req.ProtoFile {
		if slices.Contains(names, fdp.GetName()) {
			req.SourceFileDescriptors = append(req.SourceFileDescriptors, fdp)
		}
	}
	return req
}

// parameter returns the plugin parameter: `paths=source_relative` and, with the
// plugins enabled, an M mapping per design file that puts its Go package under
// the pb directory; a `;name` suffix on `go_package` still names the package.
func parameter(names []string, opts Options) string {
	params := []string{"paths=source_relative"}
	if !opts.pbEnabled() {
		return params[0]
	}
	for _, name := range names {
		params = append(params, "M"+name+"="+pbImportPath(opts, name))
	}
	return strings.Join(params, ",")
}

// pbImportPath returns the Go import path of a design file's pb package: the
// module, the pb directory, then the file's directory.
func pbImportPath(opts Options, name string) string {
	return path.Join(opts.Module, opts.pbRel(), path.Dir(name))
}
