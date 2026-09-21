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
// every server package one (`<dir>grpc`), suffixing a repeat so two
// packages of one name coexist in the file.
func buildWiringGRPCData(protos *protodesign.Set, cfg *config.Config) wiringGRPCData {
	aliases := newAliasTable()
	d := wiringGRPCData{SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext))}
	for _, svc := range protos.Services {
		serverImport := goImportFromRel(cfg.Package, cfg.Output.GRPC) + "/" + svc.Dir
		d.Services = append(d.Services, wiringGRPCService{
			Service:     svc.Name,
			PBAlias:     aliases.claim(svc.Package+"pb", svc.PBImport),
			ServerAlias: aliases.claim(strings.NewReplacer("_", "", "-", "").Replace(svc.Dir)+"grpc", serverImport),
		})
	}
	d.Imports = aliases.imports()
	return d
}

// aliasTable hands out one alias per import path and keeps them
// distinct.
type aliasTable struct {
	byPath map[string]string
	taken  map[string]bool
}

func newAliasTable() *aliasTable {
	return &aliasTable{byPath: map[string]string{}, taken: map[string]bool{}}
}

// claim returns the alias for path, allocating base (or base plus a
// counter) on first sight.
func (t *aliasTable) claim(base, path string) string {
	if alias, ok := t.byPath[path]; ok {
		return alias
	}
	alias := base
	for n := 2; t.taken[alias]; n++ {
		alias = base + strconvItoa(n)
	}
	t.taken[alias] = true
	t.byPath[path] = alias
	return alias
}

// imports lists the claimed imports sorted by path.
func (t *aliasTable) imports() []extraImport {
	out := make([]extraImport, 0, len(t.byPath))
	for path, alias := range t.byPath {
		out = append(out, extraImport{Alias: alias, Path: path})
	}
	sortExtraImports(out)
	return out
}
