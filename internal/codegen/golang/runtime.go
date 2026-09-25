package golang

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// runtimeData is the template input for the config and svccontext scaffolds.
type runtimeData struct {
	OperationName string
	ConfigImport  string
	// ConfigDir is output.config, where config.go reads config.yaml from.
	ConfigDir string
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
	dir := outputsOf(cfg).config
	data := buildRuntimeData(proj, protos, cfg)
	if err := writeGoOnce(dir.at(projectRoot, "config.go"), tmpl("config.go.tmpl"), data); err != nil {
		return err
	}
	if err := writeTextOnce(dir.at(projectRoot, "config.yaml"), tmpl("config.yaml.tmpl"), data); err != nil {
		return err
	}
	return writeTextOnce(dir.at(projectRoot, "example.config.yaml"), tmpl("example.config.yaml.tmpl"), data)
}

// buildRuntimeData is the input of the config scaffolds.
func buildRuntimeData(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config) runtimeData {
	dir := outputsOf(cfg).config
	return runtimeData{
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  dir.pkg,
		ConfigDir:     relDir(dir.rel),
		HasGRPC:       protos.HasServices(),
		HasHTTP:       projectHasRoutes(proj) || !protos.HasServices(),
	}
}

// generateSvccontext writes the gen-once output.svccontext file, whose ServiceContext embeds the
// Middlewares struct regenerated beside it.
func generateSvccontext(cfg *config.Config, projectRoot string) error {
	if cfg.Output.RuntimeDisabled() {
		return nil
	}
	data := runtimeData{
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  outputsOf(cfg).config.pkg,
	}
	return writeGoOnce(filepath.Join(projectRoot, cfg.Output.Svccontext), tmpl("svccontext.go.tmpl"), data)
}
