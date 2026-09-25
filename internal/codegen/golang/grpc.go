package golang

import (
	"fmt"
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
	ServiceImport string
	// Imports are the pb packages of the RPC's messages.
	Imports []goImport
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
		if err := writeGo(filepath.Join(dir, grpcServerFile+".go"), tmpl("grpc_server.tmpl"), buildGRPCServerData(svc, imps)); err != nil {
			return err
		}
		for _, m := range svc.Methods {
			if err := writeGo(filepath.Join(dir, m.File+".go"), tmpl("grpc_method.tmpl"), buildGRPCMethodData(svc, m, imps)); err != nil {
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
	set := grpcImportSet(svc, grpcMethodNames)
	return grpcMethodData{
		Package:       svc.Package,
		Method:        m.Name,
		FullMethod:    m.FullMethod,
		ServiceName:   logicTypeName(m.Name),
		Doc:           m.Doc,
		Sig:           buildGRPCSignature(m, set.protoType(m.In), set.protoType(m.Out)),
		ServiceImport: imps.Service,
		Imports:       set.imports(),
	}
}

// generateGRPCServices writes each RPC's gen-once logic scaffold, rendered from service.tmpl.
func generateGRPCServices(protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	for _, svc := range protos.Services {
		dir := grpcServiceDir(projectRoot, cfg, svc)
		imps := grpcImportsFor(cfg, svc)
		for _, m := range svc.Methods {
			if err := writeGoOnce(filepath.Join(dir, m.File+".go"), tmpl("service.tmpl"), buildGRPCServiceData(svc, m, imps)); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildGRPCServiceData leaves every HTTP-only field zero, so service.tmpl renders the plain entry point.
func buildGRPCServiceData(svc *protodesign.Service, m *protodesign.Method, imps grpcImports) serviceData {
	set := grpcImportSet(svc, serviceNames)
	sig := buildGRPCSignature(m, set.protoType(m.In), set.protoType(m.Out))
	return serviceData{
		Package:          svc.Package,
		Service:          svc.Name,
		Method:           m.Name,
		ServiceName:      logicTypeName(m.Name),
		Doc:              m.Doc,
		Notes:            streamNotes(m.Kind),
		Sig:              sig.Logic,
		SvccontextImport: imps.Svccontext,
		PBImports:        set.imports(),
	}
}

// grpcImportSet returns the set of a file of svc whose template binds names: svc's own messages
// under [pbAlias].
func grpcImportSet(svc *protodesign.Service, names []string) *importSet {
	return newImportSet(nil, goImport{Alias: pbAlias, Path: svc.PBImport}, names)
}

// protoType spells a message as `<alias>.<Name>`, importing its package: the set's own messages
// under their fixed alias, another package's under its Go package name.
func (s *importSet) protoType(ref protodesign.TypeRef) string {
	alias := ref.Package
	if ref.ImportPath == s.home.Path {
		alias = s.home.Alias
	}
	return s.add(alias, ref.ImportPath) + "." + ref.Name
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
