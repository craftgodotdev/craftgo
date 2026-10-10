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

// thirdParty checks the design protos of req while the plugins run. A proto whose Go code
// the runtime links in already is a problem: generating it again gives the binary a second
// copy, which panics at startup when both register. A proto whose go_package names a
// package of another module is generated as before, its path ignored, with a warning, since
// the same panic follows only when that module is linked in too.
func thirdParty(req *pluginpb.CodeGeneratorRequest, names []string, opts Options) (problems, warnings []string) {
	if !opts.pbEnabled() {
		return nil, nil
	}
	byName := map[string]*descriptorpb.FileDescriptorProto{}
	for _, fd := range req.GetProtoFile() {
		byName[fd.GetName()] = fd
	}
	for _, name := range names {
		goPath, _ := splitGoPackage(byName[name].GetOptions().GetGoPackage())
		switch {
		case strings.HasPrefix(name, "google/protobuf/"):
			problems = append(problems, fmt.Sprintf("%s copies a well-known type, which any proto imports as it is - delete the copy, whose generated Go code would register the file a second time and panic at startup", name))
		case linkedByRuntime(name):
			problems = append(problems, fmt.Sprintf("%s is a proto whose Go code the craftgo runtime links in already, so generating it again panics at startup - move it under a proto.includes root, whose protos are imported, not generated", name))
		case foreignPackage(goPath, opts.Module):
			warnings = append(warnings, fmt.Sprintf("%s has go_package %s, a package of another module: its path is ignored and its Go code generated under output.pb, so if that module is linked into the binary too, both copies register the proto and the binary panics at init - move the proto under a proto.includes root to import that package instead", name, goPath))
		}
	}
	return problems, warnings
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
