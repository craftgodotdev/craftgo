// Wiring umbrella: the one call main.go makes to attach the design.
package golang

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// wiringData is the template input for `wiring.tmpl`.
type wiringData struct {
	RoutesImport     string
	TransportImport  string
	SvccontextImport string
	HasRoutes        bool
	HasConsumers     bool
	// HasEvents gates the nil-bus guard: publishing needs the bus as much
	// as consuming does.
	HasEvents bool
	// EventSummary names what the design declares, for the nil-bus error.
	EventSummary string
}

// generateWiring writes the wiring package: one `Register` call attaching
// every HTTP route and event consumer the design declares.
//
// It is emitted for every project, including one declaring neither, because
// main.go is written once and calls it unconditionally.
func generateWiring(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	consumers, published := 0, 0
	hasEvents := eventsEnabled(proj, cfg)
	if hasEvents {
		consumers = len(proj.Consumers())
		published = len(proj.Events())
	}
	data := wiringData{
		SvccontextImport: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
		HasRoutes:        projectHasRoutes(proj),
		HasConsumers:     consumers > 0,
		HasEvents:        hasEvents,
		EventSummary:     eventSummary(published, consumers),
	}
	if data.HasRoutes {
		data.RoutesImport = goImportFromRel(cfg.Package, cfg.Output.Routes)
	}
	if data.HasConsumers {
		data.TransportImport = goImportFromRel(cfg.Package, cfg.Output.Transport)
	}
	dir := filepath.Join(projectRoot, cfg.Output.Wiring)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeRendered(dir, "wiring.go", "wiring.tmpl", data)
}

// eventSummary phrases what the design declares for the nil-bus error, so
// the reader is told which half of the wiring they are missing.
func eventSummary(published, consumers int) string {
	switch {
	case consumers == 0:
		return fmt.Sprintf("%d event(s)", published)
	case published == 0:
		return fmt.Sprintf("%d consumer(s)", consumers)
	}
	return fmt.Sprintf("%d event(s) and %d consumer(s)", published, consumers)
}
