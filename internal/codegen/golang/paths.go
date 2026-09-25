package golang

import (
	"iter"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// servicePackage lower-cases a service name ("UserService" -> "userservice").
func servicePackage(svcName string) string { return strings.ToLower(svcName) }

// servicePkgName is the Go package name of a service's handler, logic and routes files: the DSL
// package's name, or servicePackage for an unnamed package.
func servicePkgName(pkgName, svcName string) string {
	if pkgName == "" {
		return servicePackage(svcName)
	}
	return pkgName
}

// goImportFromRel turns a project-relative directory ("./internal/handler") into its import
// path under modulePath.
func goImportFromRel(modulePath, rel string) string {
	if rel = relDir(rel); rel != "" {
		return modulePath + "/" + rel
	}
	return modulePath
}

// relDir spells a project-relative directory ("./internal/config/") as forward-slash segments
// ("internal/config"), "" for the project root.
func relDir(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.TrimPrefix(rel, "/")
	return strings.TrimSuffix(rel, "/")
}

// fileDirRel returns the forward-slash directory of a project-relative file path, "" for a root file.
func fileDirRel(filePath string) string {
	filePath = strings.ReplaceAll(filePath, "\\", "/")
	dir := path.Dir(filePath)
	if dir == "." {
		return ""
	}
	return dir
}

// outputDir is a directory the Go target writes into, relative to the project root as the manifest
// spells it, and the import path of the package it holds.
type outputDir struct {
	rel, pkg string
}

// at is d under projectRoot, joined with elem.
func (d outputDir) at(projectRoot string, elem ...string) string {
	return filepath.Join(append([]string{projectRoot, d.rel}, elem...)...)
}

// sub is the directory seg, in forward slashes, under d.
func (d outputDir) sub(seg string) outputDir {
	return outputDir{rel: path.Join(d.rel, seg), pkg: d.pkg + "/" + seg}
}

// outputs are the Go target's output keys; svccontext is the directory of output.svccontext's file.
type outputs struct {
	types, transport, routes, service, grpc, wiring, svccontext, config, middleware outputDir
}

// outputsOf reads cfg's output keys.
func outputsOf(cfg *config.Config) outputs {
	dir := func(rel string) outputDir { return outputDir{rel: rel, pkg: goImportFromRel(cfg.Package, rel)} }
	return outputs{
		types:      dir(cfg.Output.Types),
		transport:  dir(cfg.Output.Transport),
		routes:     dir(cfg.Output.Routes),
		service:    dir(cfg.Output.Service),
		grpc:       dir(cfg.Output.GRPC),
		wiring:     dir(cfg.Output.Wiring),
		svccontext: dir(fileDirRel(cfg.Output.Svccontext)),
		config:     dir(cfg.Output.Config),
		middleware: dir(cfg.Output.Middleware),
	}
}

// outputKey is one output key of the Go target by its manifest name.
type outputKey struct {
	name string
	dir  outputDir
	// regenerated reports a directory whose files every run rewrites, which the sweep walks.
	regenerated bool
	// application reports a directory a contracts project writes nothing into.
	application bool
}

// keys lists o by manifest name.
func (o outputs) keys() []outputKey {
	return []outputKey{
		{"output.types", o.types, true, false},
		{"output.transport", o.transport, true, true},
		{"output.routes", o.routes, true, true},
		{"output.service", o.service, false, true},
		{"output.grpc", o.grpc, true, true},
		{"output.wiring", o.wiring, true, true},
		{"output.middleware", o.middleware, false, true},
		{"output.config", o.config, false, true},
		{"output.svccontext", o.svccontext, true, true},
	}
}

// importPaths are the import paths the files of one service segment name.
type importPaths struct {
	Types      string
	Transport  string
	Service    string
	Svccontext string
}

// segmentImports returns the import paths of segment seg of DSL package pkgName.
func (o outputs) segmentImports(pkgName, seg string) importPaths {
	return importPaths{
		Types:      o.types.sub(pkgName).pkg,
		Transport:  o.transport.sub(seg).pkg,
		Service:    o.service.sub(seg).pkg,
		Svccontext: o.svccontext.pkg,
	}
}

func httpVerb(verb string) string { return strings.ToUpper(verb) }

// outputSegFor is [route.OutputSegment]: the @group, or the service's directory when ungrouped.
func outputSegFor(svcName, group, style string) string {
	return route.OutputSegment(svcName, group, style)
}

// methodFile is the file a method's handler and logic stub are written to.
func methodFile(m *ast.Method, fileCase string) string {
	return idents.FileName(m.Name, fileCase) + ".go"
}

// segment is the methods of one service under one @group, and the directory they generate into
// under output.transport, output.service and output.routes.
type segment struct {
	pkg   *semantic.Package
	name  string
	svc   *semantic.ServiceInfo
	group string // "" when ungrouped
	dir   string
}

// methods yields s's methods in source order.
func (s segment) methods() iter.Seq[*ast.Method] {
	return func(yield func(*ast.Method) bool) {
		for _, m := range s.svc.Methods {
			if semantic.MethodGroupOf(s.svc, m) == s.group && !yield(m) {
				return
			}
		}
	}
}

// segments yields pkg's services once per @group their methods use, in service and group order.
func segments(pkg *semantic.Package, fileCase string) iter.Seq[segment] {
	return func(yield func(segment) bool) {
		for _, name := range pkg.ServiceNames() {
			svc := pkg.Services[name]
			for _, group := range distinctGroups(svc) {
				if !yield(segment{pkg: pkg, name: name, svc: svc, group: group, dir: outputSegFor(name, group, fileCase)}) {
					return
				}
			}
		}
	}
}

// projectSegments is [segments] over proj's named packages, in name order.
func projectSegments(proj *semantic.Project, fileCase string) iter.Seq[segment] {
	return func(yield func(segment) bool) {
		if proj == nil {
			return
		}
		for _, name := range proj.PackageNames() {
			pkg := proj.Packages[name]
			if pkg == nil {
				continue
			}
			for s := range segments(pkg, fileCase) {
				if !yield(s) {
					return
				}
			}
		}
	}
}

// distinctGroups returns the @groups the service's methods use, sorted, "" first.
func distinctGroups(svc *semantic.ServiceInfo) []string {
	groups := make([]string, 0, len(svc.Methods))
	for _, m := range svc.Methods {
		groups = append(groups, semantic.MethodGroupOf(svc, m))
	}
	slices.Sort(groups)
	return slices.Compact(groups)
}

// groupAliasSuffix is the import-alias suffix of a @group ("admin/ops" → "AdminOps", "" → "").
func groupAliasSuffix(group string) string {
	return idents.PascalCase(group)
}

// transportAlias is a routes file's alias for a group's transport package ("transport", "transportV2").
func transportAlias(group string) string {
	return "transport" + groupAliasSuffix(group)
}

// renderDoc renders doc as indented `//` comment lines, "" for none.
func renderDoc(doc []string, indent string) string {
	if len(doc) == 0 {
		return ""
	}
	lines := make([]string, len(doc))
	for i, line := range doc {
		lines[i] = indent + "// " + line + "\n"
	}
	return strings.Join(lines, "")
}
