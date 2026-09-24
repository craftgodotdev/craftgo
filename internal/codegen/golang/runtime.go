package golang

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"text/template"

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
	files := []struct {
		name     string
		template string
		formatGo bool
	}{
		{"config.go", "config.go.tmpl", true},
		{"config.yaml", "config.yaml.tmpl", false},
		{"example.config.yaml", "example.config.yaml.tmpl", false},
	}
	for _, f := range files {
		dest := filepath.Join(dir, f.name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		body, err := renderRuntimeTemplate(f.template, data, f.formatGo)
		if err != nil {
			return fmt.Errorf("render %s: %w", f.name, err)
		}
		if err := os.WriteFile(dest, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// generateSvccontext writes the gen-once output.svccontext file, whose ServiceContext embeds the
// Middlewares struct regenerated beside it.
func generateSvccontext(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	if cfg.Output.RuntimeDisabled() {
		return nil
	}
	dest := filepath.Join(projectRoot, cfg.Output.Svccontext)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	data := runtimeData{
		Package:       cfg.Package,
		OperationName: operationNameFor(cfg.Package),
		ConfigImport:  goImportFromRel(cfg.Package, cfg.Output.Config),
	}
	body, err := renderRuntimeTemplate("svccontext.go.tmpl", data, true)
	if err != nil {
		return fmt.Errorf("render svccontext.go: %w", err)
	}
	return os.WriteFile(dest, body, 0o644)
}

// renderRuntimeTemplate executes template name with data, gofmt-ing the result when formatGo is set.
func renderRuntimeTemplate(name string, data any, formatGo bool) ([]byte, error) {
	t, err := template.ParseFS(builtinTemplates, "templates/"+name)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute %s: %w", name, err)
	}
	if !formatGo {
		return buf.Bytes(), nil
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format %s: %w\n--- source ---\n%s", name, err, buf.String())
	}
	return formatted, nil
}
