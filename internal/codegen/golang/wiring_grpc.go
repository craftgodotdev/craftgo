package golang

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// wiringGRPCData is the template input for wiring_grpc.tmpl.
type wiringGRPCData struct {
	SvccontextImport string
	// Imports lists every pb and server package once, sorted by path.
	Imports []goImport
	// Services is one registration line per proto service.
	Services []wiringGRPCService
}

// wiringGRPCService is one `pb.RegisterXServer(srv, server.NewServer(svcCtx))`.
type wiringGRPCService struct {
	Service     string
	PBAlias     string
	ServerAlias string
}

// generateWiringGRPC writes output.wiring/grpc.go, whose RegisterGRPC attaches every proto
// service; with no proto service there is no file, and the sweep removes an old one.
func generateWiringGRPC(protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	if !protos.HasServices() {
		return nil
	}
	return writeGo(outputsOf(cfg).wiring.at(projectRoot, "grpc.go"), tmpl("wiring_grpc.tmpl"), buildWiringGRPCData(protos, cfg))
}

// buildWiringGRPCData imports each pb package as `<name>pb` and each server package as
// `<dir>grpc`; the import set numbers a clashing alias.
func buildWiringGRPCData(protos *protodesign.Set, cfg *config.Config) wiringGRPCData {
	out := outputsOf(cfg)
	imports := newImportSet(nil, goImport{}, wiringGRPCNames)
	d := wiringGRPCData{SvccontextImport: out.svccontext.pkg}
	for _, svc := range protos.Services {
		serverImport := out.grpc.sub(svc.Dir).pkg
		d.Services = append(d.Services, wiringGRPCService{
			Service:     svc.Name,
			PBAlias:     imports.add(pbAliasFor(svc.Package), svc.PBImport),
			ServerAlias: imports.add(strings.NewReplacer("_", "", "-", "").Replace(svc.Dir)+"grpc", serverImport),
		})
	}
	d.Imports = imports.imports()
	return d
}

// pbAliasFor is the wiring's alias for a pb package: its name plus `pb`,
// unless the name already ends that way (`greetpb`).
func pbAliasFor(pkg string) string {
	if strings.HasSuffix(pkg, "pb") {
		return pkg
	}
	return pkg + "pb"
}
