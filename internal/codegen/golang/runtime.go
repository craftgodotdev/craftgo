package golang

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// runtimeData is the template input for the config and svccontext scaffolds.
type runtimeData struct {
	Package       string
	OperationName string
	ConfigImport  string
	// HasGRPC adds the `grpc:` config block.
	HasGRPC bool
	// HasHTTP keeps the `server:` and `docs:` blocks; it is false only for gRPC services without a route.
	HasHTTP bool
}

// generateRuntimeConfig writes the gen-once config.go, config.yaml and example.config.yaml
// under output.config.
func generateRuntimeConfig(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	if cfg.Output.RuntimeDisabled() {
		return nil
	}
	dir := filepath.Join(projectRoot, cfg.Output.Config)
	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  goImportFromRel(cfg.Package, cfg.Output.Config),
		HasGRPC:       protos.HasServices(),
		HasHTTP:       projectHasRoutes(proj) || !protos.HasServices(),
	}
	if err := writeGoOnce(filepath.Join(dir, "config.go"), tmpl("config.go.tmpl"), data); err != nil {
		return err
	}
	if err := writeTextOnce(filepath.Join(dir, "config.yaml"), tmpl("config.yaml.tmpl"), data); err != nil {
		return err
	}
	return writeTextOnce(filepath.Join(dir, "example.config.yaml"), tmpl("example.config.yaml.tmpl"), data)
}

// generateSvccontext writes the gen-once output.svccontext file, whose ServiceContext embeds the
// Middlewares struct regenerated beside it.
func generateSvccontext(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	if cfg.Output.RuntimeDisabled() {
		return nil
	}
	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  goImportFromRel(cfg.Package, cfg.Output.Config),
	}
	return writeGoOnce(filepath.Join(projectRoot, cfg.Output.Svccontext), tmpl("svccontext.go.tmpl"), data)
}
