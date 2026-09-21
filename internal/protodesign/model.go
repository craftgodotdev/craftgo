// Package protodesign reads the gRPC half of a design: the `.proto` files
// under the design folder. It compiles them in-process with protocompile,
// hands the descriptors to protogen - the same library protoc-gen-go is
// built on - and reports what the Go generator will emit: one [Service]
// per proto service, its Go name, its package, and each RPC's Go request
// and response types. Nothing here derives a Go name by hand; every name
// the scaffolds spell is protogen's, so they agree with the pb code the
// plugins write from the same request.
//
// The package is manifest-blind, like semantic: [Options] carries the
// few facts it needs, and the caller builds it from the manifest.
package protodesign

import (
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

// Options is what a compilation needs to know about the project.
type Options struct {
	// Module is the Go import prefix of the project root.
	Module string
	// PBDir is the plugin output directory, relative to the project root
	// (`./internal/pb`). Empty disables the plugins: no M mapping is added
	// and every design file must carry `option go_package`.
	PBDir string
	// Includes lists extra import roots, absolute paths. The design root
	// is always the first import root.
	Includes []string
	// FileCase is the manifest's `output.fileCase`; it names the per-service
	// directory and the per-RPC file the scaffolds are written to.
	FileCase string
	// PluginGo and PluginGoGrpc name the plugin commands the manifest
	// chose under proto.plugins. Empty runs the tool go.mod pins.
	PluginGo     string
	PluginGoGrpc string
}

// PBEnabled reports whether the plugins run for this project.
func (o Options) PBEnabled() bool { return o.PBDir != "" }

// Set is one compiled design: every proto under the design root and the
// services they declare.
type Set struct {
	// Options is what the set was compiled with.
	Options Options
	// Root is the design root the names are relative to.
	Root string
	// Names lists the design files, slash-separated relative to Root, in
	// sorted order. They are the files the plugins generate for.
	Names []string
	// Plugin is the protogen view of the whole graph; the design files are
	// the ones with Generate set.
	Plugin *protogen.Plugin
	// Request is the plugin request, deps first. The plugins receive it
	// verbatim.
	Request *pluginpb.CodeGeneratorRequest
	// Services lists every service the design files declare, sorted by
	// proto full name.
	Services []*Service
}

// Service is one proto service and what the scaffolds need to spell it.
type Service struct {
	Desc *protogen.Service
	// Name is the Go name protogen gives the service (`Greeter`).
	Name string
	// FullName is the proto full name (`greet.Greeter`).
	FullName string
	// Package is the Go package name of the file declaring the service.
	// The server layer and the logic scaffolds declare it, the way the
	// HTTP layers declare the design package.
	Package string
	// PBImport is the Go import path of the generated pb package.
	PBImport string
	// Dir is the per-service output directory name, derived from Name by
	// the manifest's file case.
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

// String names the kind the way the proto keyword does.
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
	Desc *protogen.Method
	// Name is the Go method name (`SayHello`).
	Name string
	// File is the per-RPC file name without extension, derived from Name
	// by the manifest's file case.
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
	// Package is the Go package name of the file declaring the message,
	// the alias generated code imports it under when it is not the
	// service's own pb package.
	Package string
}

// HasServices reports whether any design file declares a service. A set
// of message-only files still has pb code to generate, but nothing for
// the application layers.
func (s *Set) HasServices() bool { return s != nil && len(s.Services) > 0 }

// PBFiles names the files the plugins write under pbRoot for this set:
// `<prefix>.pb.go` for every design file and `<prefix>_grpc.pb.go` for
// the ones declaring a service, where the prefix is the file name minus
// `.proto` (protoc-gen-go's `paths=source_relative` rule). It is what the
// output sweep keeps, so it is predicted here rather than read back.
// With the plugins disabled nothing is written, and nothing is listed.
func (s *Set) PBFiles(pbRoot string) []string {
	if s == nil || !s.Options.PBEnabled() {
		return nil
	}
	var out []string
	for _, name := range s.Names {
		prefix := strings.TrimSuffix(name, ".proto")
		out = append(out, filepath.Join(pbRoot, filepath.FromSlash(prefix+".pb.go")))
		if f := s.Plugin.FilesByPath[name]; f != nil && len(f.Services) > 0 {
			out = append(out, filepath.Join(pbRoot, filepath.FromSlash(prefix+"_grpc.pb.go")))
		}
	}
	return out
}
