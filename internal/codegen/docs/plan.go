package docs

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// RegeneratedFile is the document [GenerateOpenAPI] writes, or "" when
// `output.openapi` is off.
func RegeneratedFile(cfg *config.Config, projectRoot string) string {
	dest := cfg.Output.OpenAPI
	if dest == "" || dest == "-" {
		return ""
	}
	return filepath.Join(projectRoot, dest)
}
