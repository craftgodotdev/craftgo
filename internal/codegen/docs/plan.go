package docs

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// RegeneratedFile is the document [GenerateOpenAPI] writes, or "" when it
// writes none: `output.openapi` is off, or the design has no DSL package.
func RegeneratedFile(proj *semantic.Project, cfg *config.Config, projectRoot string) string {
	if !describable(proj) {
		return ""
	}
	return OutputDir(cfg, projectRoot)
}

// OutputDir is the document's path, whether or not this run writes it, or ""
// when `output.openapi` is off.
func OutputDir(cfg *config.Config, projectRoot string) string {
	dest := cfg.Output.OpenAPI
	if dest == "" || dest == "-" {
		return ""
	}
	return filepath.Join(projectRoot, dest)
}

// describable reports whether proj has a DSL package to document; a design
// of protos alone has none.
func describable(proj *semantic.Project) bool {
	return proj != nil && len(proj.Packages) > 0
}
