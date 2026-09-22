package golang

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// wiringGRPCData is the template input for `wiring_grpc.tmpl`.
type wiringGRPCData struct {
	SvccontextImport string
	// Imports lists every pb and server package once, sorted by path.
	Imports []extraImport
	// Services is one registration line per proto service.
	Services []wiringGRPCService
}

// wiringGRPCService is one `pb.RegisterXServer(srv, server.NewServer(svcCtx))`.
type wiringGRPCService struct {
	Service     string
	PBAlias     string
	ServerAlias string
}

// generateWiringGRPC writes `grpc.go` beside wiring.go: the RegisterGRPC
// call attaching every proto service. It is written only when a proto
// declares a service, and swept with the wiring directory otherwise,
// so an HTTP-only project's wiring package never imports the gRPC
// runtime.
func generateWiringGRPC(protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	if !protos.HasServices() {
		return nil
	}
	dir := filepath.Join(projectRoot, cfg.Output.Wiring)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeRendered(dir, "grpc.go", "wiring_grpc.tmpl", buildWiringGRPCData(protos, cfg))
}

// buildWiringGRPCData gives every pb package one alias (`<name>pb`) and
// every server package one (`<dir>grpc`) through the file's import set,
// so two packages of one name coexist in the file.
func buildWiringGRPCData(protos *protodesign.Set, cfg *config.Config) wiringGRPCData {
	imports := newGRPCImportSet()
	d := wiringGRPCData{SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext))}
	for _, svc := range protos.Services {
		serverImport := goImportFromRel(cfg.Package, cfg.Output.GRPC) + "/" + svc.Dir
		imports.add(extraImport{Alias: pbAliasFor(svc.Package), Path: svc.PBImport})
		imports.add(extraImport{Alias: strings.NewReplacer("_", "", "-", "").Replace(svc.Dir) + "grpc", Path: serverImport})
		d.Services = append(d.Services, wiringGRPCService{
			Service:     svc.Name,
			PBAlias:     imports.aliasFor(svc.PBImport),
			ServerAlias: imports.aliasFor(serverImport),
		})
	}
	d.Imports = imports.sorted()
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
