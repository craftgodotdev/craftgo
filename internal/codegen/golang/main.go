package golang

import (
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// mainData is the template input for main.tmpl.
type mainData struct {
	ConfigImport string
	// ConfigDir is output.config, where example.config.yaml documents config.yaml.
	ConfigDir        string
	WiringImport     string
	MiddlewareImport string
	SvccontextImport string
	Middlewares      []string
	// HasDocs embeds and serves the OpenAPI document of a design with routes; go:embed takes it
	// only from disk and under main.go's directory.
	HasDocs bool
	// OpenAPIEmbed is the document's forward-slash path relative to main.go, for go:embed.
	OpenAPIEmbed string
	// HasRoutes adds the HTTP listener and its wiring.Register call.
	HasRoutes bool
	// HasGRPC adds the gRPC listener and its wiring.RegisterGRPC call.
	HasGRPC bool
}

// generateProjectMain writes the gen-once output.main when the design has a route or an RPC to serve.
func generateProjectMain(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	if proj == nil {
		return nil
	}
	if cfg.Output.RuntimeDisabled() {
		return nil
	}
	if !projectHasRoutes(proj) && !protos.HasServices() {
		return nil
	}
	data := buildProjectMainData(proj, protos, cfg)
	if _, err := os.Stat(filepath.Join(projectRoot, cfg.Output.OpenAPI)); err != nil {
		data.HasDocs = false
	}
	return writeGoOnce(filepath.Join(projectRoot, cfg.Output.Main), tmpl("main.tmpl"), data)
}

// projectHasRoutes reports whether any service declares an HTTP method.
func projectHasRoutes(proj *semantic.Project) bool {
	for _, pkg := range proj.Packages {
		if pkg == nil {
			continue
		}
		for _, svc := range pkg.Services {
			if len(svc.Methods) > 0 {
				return true
			}
		}
	}
	return false
}

// buildProjectMainData lists every package's middlewares once, in package order.
func buildProjectMainData(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config) mainData {
	out := outputsOf(cfg)
	d := mainData{
		ConfigImport:     out.config.pkg,
		ConfigDir:        displayDir(out.config.rel),
		WiringImport:     out.wiring.pkg,
		MiddlewareImport: out.middleware.pkg,
		SvccontextImport: out.svccontext.pkg,
		HasRoutes:        projectHasRoutes(proj),
		HasGRPC:          protos.HasServices(),
	}
	seen := map[string]bool{}
	for _, k := range slices.Sorted(maps.Keys(proj.Packages)) {
		p := proj.Packages[k]
		if p == nil {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(p.Middlewares)) {
			if seen[name] {
				continue
			}
			seen[name] = true
			d.Middlewares = append(d.Middlewares, name)
		}
	}

	if d.HasRoutes && !cfg.Output.OpenAPIDisabled() {
		d.OpenAPIEmbed, d.HasDocs = DocsEmbed(cfg)
	}
	return d
}

// DocsEmbed returns the path of the OpenAPI document relative to output.main's
// directory, as main.go's go:embed names it, and false when go:embed cannot
// reach the document from there.
func DocsEmbed(cfg *config.Config) (string, bool) {
	rel, err := filepath.Rel(filepath.Dir(filepath.Clean(cfg.Output.Main)), filepath.Clean(cfg.Output.OpenAPI))
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// operationNameFor is the config's default serviceName: the module path's last segment
// (`github.com/foo/myapp` → `myapp`), or "api" for an empty path.
func operationNameFor(modulePath string) string {
	for i := len(modulePath) - 1; i >= 0; i-- {
		if modulePath[i] == '/' {
			return modulePath[i+1:]
		}
	}
	if modulePath == "" {
		return "api"
	}
	return modulePath
}
