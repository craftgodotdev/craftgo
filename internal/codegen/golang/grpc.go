package golang

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The gRPC half of the application: for every proto service, a server
// package under output.grpc (regenerated - the struct implementing the
// generated interface and one file per RPC delegating to logic) and the
// logic scaffolds under output.service (written once), the same split
// the HTTP transport and service layers make.

// grpcImports bundles the import paths one proto service's files use.
type grpcImports struct {
	PB         string
	Service    string
	Svccontext string
}

// pbAlias is the import alias every generated file gives the service's
// own pb package; a message from another proto package is imported
// under that package's own name.
const pbAlias = "pb"

// grpcServerFile is the file holding the server struct, beside the
// per-RPC files; an RPC whose file would take the name is rejected.
const grpcServerFile = "server"

// logicTypeName is the name of the per-RPC logic struct, the rule the
// HTTP scaffold sets for its methods.
func logicTypeName(method string) string { return method + "Service" }

// grpcServerData is the template input for `grpc_server.tmpl`.
type grpcServerData struct {
	Package          string
	Service          string
	FullName         string
	PBImport         string
	SvccontextImport string
}

// grpcMethodData is the template input for `grpc_method.tmpl`.
type grpcMethodData struct {
	Package       string
	Method        string
	FullMethod    string
	ServiceName   string
	Doc           []string
	Sig           grpcSignature
	NeedsPB       bool
	PBImport      string
	ServiceImport string
	ExtraImports  []extraImport
}

// grpcImportsFor computes the import paths of one proto service.
func grpcImportsFor(cfg *config.Config, svc *protodesign.Service) grpcImports {
	return grpcImports{
		PB:         svc.PBImport,
		Service:    goImportFromRel(cfg.Package, cfg.Output.Service) + "/" + svc.Dir,
		Svccontext: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
}

// grpcServerDir is where a proto service's server package is written.
func grpcServerDir(projectRoot string, cfg *config.Config, svc *protodesign.Service) string {
	return filepath.Join(projectRoot, cfg.Output.GRPC, svc.Dir)
}

// grpcServiceDir is where a proto service's logic scaffolds are written.
func grpcServiceDir(projectRoot string, cfg *config.Config, svc *protodesign.Service) string {
	return filepath.Join(projectRoot, cfg.Output.Service, svc.Dir)
}

// generateGRPCServers writes the server package of every proto service:
// server.go and one file per RPC, all regenerated.
func generateGRPCServers(protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	for _, svc := range protos.Services {
		dir := grpcServerDir(projectRoot, cfg, svc)
		imps := grpcImportsFor(cfg, svc)
		if err := writeRendered(dir, grpcServerFile+".go", "grpc_server.tmpl", buildGRPCServerData(svc, imps)); err != nil {
			return err
		}
		for _, m := range svc.Methods {
			if err := writeRendered(dir, m.File+".go", "grpc_method.tmpl", buildGRPCMethodData(svc, m, imps)); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildGRPCServerData populates the server struct input for one service.
func buildGRPCServerData(svc *protodesign.Service, imps grpcImports) grpcServerData {
	return grpcServerData{
		Package:          svc.Package,
		Service:          svc.Name,
		FullName:         svc.FullName,
		PBImport:         imps.PB,
		SvccontextImport: imps.Svccontext,
	}
}

// buildGRPCMethodData populates the server-layer input for one RPC.
func buildGRPCMethodData(svc *protodesign.Service, m *protodesign.Method, imps grpcImports) grpcMethodData {
	refs := newTypeRefs(svc)
	d := grpcMethodData{
		Package:       svc.Package,
		Method:        m.Name,
		FullMethod:    "/" + svc.FullName + "/" + string(m.Desc.Desc.Name()),
		ServiceName:   logicTypeName(m.Name),
		Doc:           m.Doc,
		Sig:           buildGRPCSignature(m, refs.render(m.In), refs.render(m.Out)),
		PBImport:      imps.PB,
		ServiceImport: imps.Service,
	}
	d.NeedsPB, d.ExtraImports = refs.usedOwn, refs.imports.sorted()
	return d
}

// generateGRPCServices scaffolds the logic of every proto service: one
// `<rpc>.go` per RPC under output.service/<service>, written once, from
// the same template the HTTP logic scaffolds use.
func generateGRPCServices(protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	for _, svc := range protos.Services {
		dir := grpcServiceDir(projectRoot, cfg, svc)
		imps := grpcImportsFor(cfg, svc)
		for _, m := range svc.Methods {
			if err := writeScaffoldOnce(filepath.Join(dir, m.File+".go"), "service.tmpl", buildGRPCServiceData(svc, m, imps)); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildGRPCServiceData populates the logic-scaffold input for one RPC.
// Every HTTP-only field stays at its zero value, so the template renders
// the plain entry-point shape.
func buildGRPCServiceData(svc *protodesign.Service, m *protodesign.Method, imps grpcImports) serviceData {
	refs := newTypeRefs(svc)
	sig := buildGRPCSignature(m, refs.render(m.In), refs.render(m.Out))
	d := serviceData{
		Package:          svc.Package,
		Service:          svc.Name,
		Method:           m.Name,
		ServiceName:      logicTypeName(m.Name),
		Doc:              m.Doc,
		Notes:            streamNotes(m.Kind),
		Sig:              sig.Logic,
		SvccontextImport: imps.Svccontext,
	}
	if refs.usedOwn {
		d.PBImports = append(d.PBImports, extraImport{Alias: pbAlias, Path: imps.PB})
	}
	d.PBImports = append(d.PBImports, refs.imports.sorted()...)
	return d
}

// typeRefs renders message types for one service's files: the service's
// own pb package under [pbAlias], every other under its package name,
// through the file's import set so the aliases stay distinct.
type typeRefs struct {
	own     string
	imports *importSet
	usedOwn bool
}

func newTypeRefs(svc *protodesign.Service) *typeRefs {
	return &typeRefs{own: svc.PBImport, imports: newGRPCImportSet()}
}

// render spells ref as `<alias>.<Name>`.
func (r *typeRefs) render(ref protodesign.TypeRef) string {
	if ref.ImportPath == r.own {
		r.usedOwn = true
		return pbAlias + "." + ref.Name
	}
	r.imports.add(extraImport{Alias: ref.Package, Path: ref.ImportPath})
	return r.imports.aliasFor(ref.ImportPath) + "." + ref.Name
}

// writeScaffoldOnce renders tmplName into path unless a file is already
// there: the scaffold is the user's once written.
func writeScaffoldOnce(path, tmplName string, data any) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl(tmplName), data)
	if err != nil {
		return fmt.Errorf("render %s: %w", filepath.Base(path), err)
	}
	return os.WriteFile(path, formatted, 0o644)
}

// ValidateProtoOutputs rejects what the gRPC emitters would write over
// each other: a proto service whose logic directory a DSL service already
// owns (both would write `<output.service>/<dir>`, with different package
// clauses), an RPC whose file would take the server struct's, and an RPC
// whose logic type another RPC's constructor is named after (`X` beside
// `NewX`). The DSL side's own collisions are the analyser's, the proto
// side's are protodesign's; this is the one check that sees the emitted
// names.
func ValidateProtoOutputs(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config) error {
	if protos == nil {
		return nil
	}
	for _, svc := range protos.Services {
		types := map[string]string{}
		for _, m := range svc.Methods {
			if m.File == grpcServerFile {
				return fmt.Errorf("%s: rpc %s would generate file %s.go, which holds the server struct - rename it", svc.FullName, m.Name, grpcServerFile)
			}
			types[logicTypeName(m.Name)] = m.Name
		}
		for _, m := range svc.Methods {
			if other, ok := types["New"+logicTypeName(m.Name)]; ok {
				return fmt.Errorf("%s: rpcs %s and %s generate a logic constructor and a logic type of one name, New%s - rename one", svc.FullName, m.Name, other, logicTypeName(m.Name))
			}
		}
	}
	if proj == nil {
		return nil
	}
	owners := map[string]string{}
	for _, name := range sortedPackageNames(proj) {
		pkg := proj.Packages[name]
		if pkg == nil {
			continue
		}
		for _, svcName := range sortedServices(pkg) {
			for _, group := range distinctGroups(pkg.Services[svcName]) {
				owners[filepath.ToSlash(outputSegFor(svcName, group, cfg.Output.FileCase))] = svcName
			}
		}
	}
	for _, svc := range protos.Services {
		if owner, ok := owners[svc.Dir]; ok {
			return fmt.Errorf("proto service %s and service %s both generate into %s/%s - rename one", svc.FullName, owner, cfg.Output.Service, svc.Dir)
		}
	}
	return nil
}
