package protodesign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	names = withoutIncluded(designRoot, names, opts.Includes)
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
		return nil, fmt.Errorf("proto errors:\n  %s", goPackageProblem(req, names, opts, err))
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

// withoutIncluded drops the names under an include root inside designRoot: a proto
// there is imported by its path below that root, as every include is, and is not
// generated.
func withoutIncluded(designRoot string, names, includes []string) []string {
	var nested []string
	for _, inc := range includes {
		if rel, ok := below(designRoot, inc); ok {
			nested = append(nested, rel+"/")
		}
	}
	if len(nested) == 0 {
		return names
	}
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool {
		return slices.ContainsFunc(nested, func(dir string) bool { return strings.HasPrefix(name, dir) })
	})
}

// below returns dir as a slash path relative to root when dir lies under root.
func below(root, dir string) (string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// compile runs protocompile over names with designRoot as the first import
// root, reporting every error with its file path.
func compile(ctx context.Context, designRoot string, names, includes []string) (linker.Files, error) {
	roots := append([]string{designRoot}, includes...)
	var errs []string
	rep := reporter.NewReporter(func(e reporter.ErrorWithPos) error {
		pos := e.GetPosition()
		errs = append(errs, fmt.Sprintf("  %s:%d:%d: %v", sourcePath(roots, pos.Filename), pos.Line, pos.Col, e.Unwrap()))
		return nil
	}, nil)
	c := protocompile.Compiler{
		Resolver:       protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: roots}),
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

// sourcePath returns the file the resolver reads for the proto at slash path name: under the
// first of roots that holds it, as the resolver searches them, or under the first root.
func sourcePath(roots []string, name string) string {
	rel := filepath.FromSlash(name)
	for _, root := range roots {
		if p := filepath.Join(root, rel); fileExists(p) {
			return p
		}
	}
	return filepath.Join(roots[0], rel)
}

// fileExists reports whether path names something other than a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
	for line := range strings.SplitSeq(strings.TrimRight(string(c), "\n"), "\n") {
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
