package docs

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Plan lists what [GenerateOpenAPI] writes under projectRoot: the document, as the one path the
// sweep covers, with its header, and as the file written, which a design with no DSL package does
// not get. With `output.openapi` off there is neither.
func Plan(proj *semantic.Project, cfg *config.Config, projectRoot string) (paths map[string][]string, files []string) {
	doc := documentPath(cfg, projectRoot)
	if doc == "" {
		return nil, nil
	}
	if describable(proj) {
		files = []string{doc}
	}
	return map[string][]string{doc: {GeneratedHeader}}, files
}

// documentPath is the path of the document, or "" when `output.openapi` is off.
func documentPath(cfg *config.Config, projectRoot string) string {
	if cfg.Output.OpenAPIDisabled() {
		return ""
	}
	return filepath.Join(projectRoot, cfg.Output.OpenAPI)
}

// describable reports whether proj has a DSL package to document; a design
// of protos alone has none.
func describable(proj *semantic.Project) bool {
	return proj != nil && len(proj.Packages) > 0
}
