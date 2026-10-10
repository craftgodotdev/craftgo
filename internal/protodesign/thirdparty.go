package protodesign

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// runtimeProtos are the proto paths, and the directories of them (ending in "/"), whose
// Go code a craftgo service links in through the runtime: protobuf-go's, grpc-go's and
// the genproto packages they import, and the telemetry stack's.
var runtimeProtos = []string{
	"google/api/httpbody.proto",
	"google/protobuf/",
	"google/rpc/",
	"grpc/binlog/",
	"grpc/health/",
	"grpc/reflection/",
	"io/prometheus/client/",
	"opentelemetry/proto/",
}

// linkedByRuntime reports whether the runtime links in the Go code of the proto at slash
// path name.
func linkedByRuntime(name string) bool {
	for _, p := range runtimeProtos {
		if name == p || strings.HasSuffix(p, "/") && strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// thirdPartyProblems names each design proto of req that is someone else's, while the
// plugins run: one whose Go code the runtime links in already, and one whose
// go_package names a package of another module. Generating either gives the binary a
// second copy of the proto's Go code, which panics at startup when both register.
func thirdPartyProblems(req *pluginpb.CodeGeneratorRequest, names []string, opts Options) []string {
	if !opts.pbEnabled() {
		return nil
	}
	byName := map[string]*descriptorpb.FileDescriptorProto{}
	for _, fd := range req.GetProtoFile() {
		byName[fd.GetName()] = fd
	}
	var out []string
	for _, name := range names {
		goPath, _ := splitGoPackage(byName[name].GetOptions().GetGoPackage())
		switch {
		case strings.HasPrefix(name, "google/protobuf/"):
			out = append(out, fmt.Sprintf("%s copies a well-known type, which any proto imports as it is - delete the copy, whose generated Go code would register the file a second time and panic at startup", name))
		case linkedByRuntime(name):
			out = append(out, fmt.Sprintf("%s is a proto whose Go code the craftgo runtime links in already, so generating it again panics at startup - move it under a proto.includes root, whose protos are imported, not generated", name))
		case foreignPackage(goPath, opts.Module):
			out = append(out, fmt.Sprintf("%s has go_package %s, outside module %s: generating its Go code here makes a second copy, which panics at startup once both are linked - drop the path from go_package to own the proto here, move it under a proto.includes root to import its package, or set output.pb to \"-\" to import what your own protoc pipeline writes", name, goPath, opts.Module))
		}
	}
	return out
}

// foreignPackage reports whether import path p names a package of another module than
// module: its first element holds a dot, as a fetchable module path's does, and p lies
// outside module's tree.
func foreignPackage(p, module string) bool {
	first, _, _ := strings.Cut(p, "/")
	if first == "." || first == ".." || !strings.Contains(first, ".") {
		return false
	}
	return p != module && !strings.HasPrefix(p, module+"/")
}
