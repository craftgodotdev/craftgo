package golang

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// grpcImports bundles the import paths one proto service's files use.
type grpcImports struct {
	PB         string
	Service    string
	Svccontext string
}

// pbAlias is the import alias of a proto service's own pb package.
const pbAlias = "pb"

// grpcServerFile is the base name of the file holding the server struct.
const grpcServerFile = "server"

// logicTypeName is the name of an RPC's logic struct.
func logicTypeName(method string) string { return method + "Service" }

// grpcServerData is the template input for grpc_server.tmpl.
type grpcServerData struct {
	Package          string
	Service          string
	FullName         string
	PBImport         string
	SvccontextImport string
}

// grpcMethodData is the template input for grpc_method.tmpl.
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

func grpcImportsFor(cfg *config.Config, svc *protodesign.Service) grpcImports {
	return grpcImports{
		PB:         svc.PBImport,
		Service:    goImportFromRel(cfg.Package, cfg.Output.Service) + "/" + svc.Dir,
		Svccontext: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
}

// grpcServerDir holds a proto service's server package.
func grpcServerDir(projectRoot string, cfg *config.Config, svc *protodesign.Service) string {
	return filepath.Join(projectRoot, cfg.Output.GRPC, svc.Dir)
}

// grpcServiceDir holds a proto service's logic scaffolds.
func grpcServiceDir(projectRoot string, cfg *config.Config, svc *protodesign.Service) string {
	return filepath.Join(projectRoot, cfg.Output.Service, svc.Dir)
}

// generateGRPCServers writes each proto service's server package: server.go and one file per RPC.
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

func buildGRPCServerData(svc *protodesign.Service, imps grpcImports) grpcServerData {
	return grpcServerData{
		Package:          svc.Package,
		Service:          svc.Name,
		FullName:         svc.FullName,
		PBImport:         imps.PB,
		SvccontextImport: imps.Svccontext,
	}
}

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

// generateGRPCServices writes each RPC's gen-once logic scaffold, rendered from service.tmpl.
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

// buildGRPCServiceData leaves every HTTP-only field zero, so service.tmpl renders the plain entry point.
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

// typeRefs renders message types for one file: the service's own under [pbAlias], every other
// under its package name.
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

// writeScaffoldOnce renders tmplName into path unless the file exists.
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

// ValidateProtoOutputs rejects gRPC output that would collide: an RPC file named like the server
// struct's, a logic type named like another RPC's constructor (`X` beside `NewX`), or a proto
// service writing into a DSL service's output.service directory.
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
	for _, name := range proj.PackageNames() {
		pkg := proj.Packages[name]
		if pkg == nil {
			continue
		}
		for _, svcName := range pkg.ServiceNames() {
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
