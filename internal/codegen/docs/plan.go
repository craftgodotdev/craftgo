package docs

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// RegeneratedFile is the document [GenerateOpenAPI] writes, or "" when
// it writes none: `output.openapi` is off, or the design holds no DSL
// package for the document to describe.
func RegeneratedFile(proj *semantic.Project, cfg *config.Config, projectRoot string) string {
	if !describable(proj) {
		return ""
	}
	return OutputDir(cfg, projectRoot)
}

// OutputDir is the directory the document lives in, or "" when
// `output.openapi` is off. The sweep walks it whether or not this run
// writes a document, so the one an earlier run left behind for a design
// that has since lost its last DSL package goes.
func OutputDir(cfg *config.Config, projectRoot string) string {
	dest := cfg.Output.OpenAPI
	if dest == "" || dest == "-" {
		return ""
	}
	return filepath.Join(projectRoot, dest)
}

// describable reports whether there is a design for the document to
// describe. A project of protos alone has none: the document would
// carry an empty `paths` and empty `components`, and nothing serves it.
func describable(proj *semantic.Project) bool {
	return proj != nil && len(proj.Packages) > 0
}
