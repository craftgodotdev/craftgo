package golang

import (
	"fmt"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// GenerateEventTarget writes the Go event library under outDir, one events.go per DSL package
// that declares events, the same for every project kind.
func GenerateEventTarget(proj *semantic.Project, cfg *config.Config, projectRoot, outDir string) error {
	for _, name := range proj.PackageNames() {
		r := buildProjectResolver(proj, cfg, name)
		if err := generatePackageEvents(proj.Packages[name], cfg, projectRoot, outDir, r); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// expectedEventFiles is the set of events.go files this run writes under root.
func expectedEventFiles(proj *semantic.Project, root string) map[string]bool {
	keep := map[string]bool{}
	for _, name := range proj.PackageNames() {
		pkg := proj.Packages[name]
		if pkg == nil || len(pkg.Events) == 0 {
			continue
		}
		keep[filepath.Join(root, name, "events.go")] = true
	}
	return keep
}
