package golang

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

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
	HasMiddlewares   bool
	// HasDocs embeds and serves the OpenAPI document when the design has routes and the document
	// sits under main.go's directory, which go:embed cannot leave.
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
	return writeGoOnce(filepath.Join(projectRoot, cfg.Output.Main), tmpl("main.tmpl"), buildProjectMainData(proj, protos, cfg))
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
	d := mainData{
		ConfigImport:     goImportFromRel(cfg.Package, cfg.Output.Config),
		ConfigDir:        relDir(cfg.Output.Config),
		WiringImport:     goImportFromRel(cfg.Package, cfg.Output.Wiring),
		MiddlewareImport: goImportFromRel(cfg.Package, cfg.Output.Middleware),
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
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
	d.HasMiddlewares = len(d.Middlewares) > 0

	if spec := cfg.Output.OpenAPI; d.HasRoutes && !cfg.Output.OpenAPIDisabled() {
		mainDir := filepath.Dir(filepath.Clean(cfg.Output.Main))
		if rel, err := filepath.Rel(mainDir, filepath.Clean(spec)); err == nil {
			rel = filepath.ToSlash(rel)
			if !strings.HasPrefix(rel, "../") {
				d.HasDocs = true
				d.OpenAPIEmbed = rel
			}
		}
	}
	return d
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
