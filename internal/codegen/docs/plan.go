package docs

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/claim"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// PlannedOutputs names the documents [GenerateOpenAPI] and
// [GenerateAsyncAPI] write, grouped by the directory each claim is filed
// in. A document turned off with `-` writes nothing and claims nothing.
func PlannedOutputs(proj *semantic.Project, cfg *config.Config, projectRoot string) []claim.Output {
	var out []claim.Output
	if dest := cfg.Output.OpenAPI; proj != nil && dest != "" && dest != "-" {
		full := filepath.Join(projectRoot, dest)
		out = append(out, claim.Output{Root: filepath.Dir(full), Files: []string{full}})
	}
	if dest := cfg.Events.AsyncAPI; dest != "" && dest != "-" && proj.HasEvents() {
		full := filepath.Join(projectRoot, dest)
		out = append(out, claim.Output{Root: filepath.Dir(full), Files: []string{full}})
	}
	return out
}
