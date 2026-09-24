package protodesign

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"github.com/bufbuild/protocompile/reporter"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/craftgodotdev/craftgo/internal/idents"
)

// Load compiles every proto under designRoot and returns the set, or nil
// when the design has no proto at all.
func Load(ctx context.Context, designRoot string, opts Options) (*Set, error) {
	names, err := discover(designRoot)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	files, err := compile(ctx, designRoot, names, opts.Includes)
	if err != nil {
		return nil, err
	}
	req := buildRequest(files, names, parameter(names, opts))
	plugin, err := protogen.Options{}.New(req)
	if err != nil {
		if !opts.pbEnabled() {
			return nil, fmt.Errorf("output.pb is \"-\", so every design proto needs `option go_package`: %w", err)
		}
		return nil, fmt.Errorf("a proto under proto.includes needs `option go_package` (the design's own protos are placed under output.pb): %w", err)
	}
	set := &Set{opts: opts, names: names, plugin: plugin, request: req}
	for _, f := range plugin.Files {
		if !f.Generate {
			continue
		}
		for _, svc := range f.Services {
			set.Services = append(set.Services, newService(f, svc, plugin, opts.FileCase))
		}
	}
	sort.Slice(set.Services, func(i, j int) bool { return set.Services[i].FullName < set.Services[j].FullName })
	if err := set.checkCollisions(); err != nil {
		return nil, err
	}
	return set, nil
}

// compile runs protocompile over names with designRoot as the first import
// root, reporting every error with its file path.
func compile(ctx context.Context, designRoot string, names, includes []string) (linker.Files, error) {
	var errs []string
	rep := reporter.NewReporter(func(e reporter.ErrorWithPos) error {
		pos := e.GetPosition()
		errs = append(errs, fmt.Sprintf("  %s:%d:%d: %v", filepath.Join(designRoot, filepath.FromSlash(pos.Filename)), pos.Line, pos.Col, e.Unwrap()))
		return nil
	}, nil)
	c := protocompile.Compiler{
		Resolver:       protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: append([]string{designRoot}, includes...)}),
		SourceInfoMode: protocompile.SourceInfoStandard,
		Reporter:       rep,
	}
	files, err := c.Compile(ctx, names...)
	if len(errs) > 0 {
		return nil, fmt.Errorf("proto errors:\n%s", strings.Join(errs, "\n"))
	}
	if err != nil {
		var panicErr protocompile.PanicError
		if errors.As(err, &panicErr) {
			return nil, fmt.Errorf("proto compiler: %v", panicErr.Value)
		}
		return nil, err
	}
	return files, nil
}

// newService reads one service off the protogen graph.
func newService(f *protogen.File, svc *protogen.Service, plugin *protogen.Plugin, fileCase string) *Service {
	s := &Service{
		Name:     svc.GoName,
		FullName: string(svc.Desc.FullName()),
		Package:  string(f.GoPackageName),
		PBImport: string(f.GoImportPath),
		Dir:      idents.FileName(svc.GoName, fileCase),
	}
	for _, m := range svc.Methods {
		s.Methods = append(s.Methods, &Method{
			FullMethod: "/" + s.FullName + "/" + string(m.Desc.Name()),
			Name:       m.GoName,
			File:       idents.FileName(m.GoName, fileCase),
			Kind:       kindOf(m.Desc),
			In:         typeRef(m.Input, plugin),
			Out:        typeRef(m.Output, plugin),
			Doc:        docLines(m.Comments.Leading),
		})
	}
	return s
}

func kindOf(d protoreflect.MethodDescriptor) Kind {
	switch {
	case d.IsStreamingClient() && d.IsStreamingServer():
		return Bidi
	case d.IsStreamingClient():
		return ClientStream
	case d.IsStreamingServer():
		return ServerStream
	}
	return Unary
}

func typeRef(m *protogen.Message, plugin *protogen.Plugin) TypeRef {
	ref := TypeRef{Name: m.GoIdent.GoName, ImportPath: string(m.GoIdent.GoImportPath)}
	if f := plugin.FilesByPath[m.Desc.ParentFile().Path()]; f != nil {
		ref.Package = string(f.GoPackageName)
	}
	return ref
}

// docLines splits a leading comment into lines, dropping blank lines at either
// end and the space protoc keeps after `//`.
func docLines(c protogen.Comments) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(c), "\n"), "\n") {
		out = append(out, strings.TrimRight(strings.TrimPrefix(line, " "), " \t"))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
