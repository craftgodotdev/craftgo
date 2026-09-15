package golang

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The Go event library, one file per DSL package under the event
// target's output:
//
//	<events>/<package>/events.go  one descriptor per contract
//
// It is pure contract: it imports the event runtime and the payload
// types and nothing else, so one library serves every deployable that
// imports it. Everything about DELIVERY - which group, which middleware,
// which process runs what - is the application's, written in its own Go.

// eventsData is the template input for one package's events.go.
type eventsData struct {
	Package string
	Imports []extraImport
	Events  []eventDescriptor
}

// eventDescriptor is one contract: its subject, and the payload the
// descriptor is typed on.
type eventDescriptor struct {
	Name        string
	ConstName   string
	Contract    string
	PayloadType string
	// Validate is the method value handed to NewEvent, or `nil` when the
	// payload type carries no generated Validate.
	Validate string
	Doc      []string
}

// generatePackageEvents writes pkg's event library under the Go event
// target's directory: one contract constant and one descriptor per event
// the package declares. A package that declares none leaves no directory
// behind.
func generatePackageEvents(pkg *semantic.Package, cfg *config.Config, projectRoot, outDir string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	r = resolverFor(pkg, r)
	dir := filepath.Join(projectRoot, outDir, pkg.Name)
	imports := newImportSet(r.CrossPkg)
	typesImport := typesImportRoot(cfg) + "/" + pkg.Name
	data := eventsData{Package: pkg.Name}
	for _, name := range sortedKeys(pkg.Events) {
		ev, ok := r.Proj.LookupEvent(pkg.Name, name)
		if !ok {
			continue
		}
		payload := imports.payloadRefType(ev.PayloadRef, typesImport)
		data.Events = append(data.Events, eventDescriptor{
			Name:        ev.Name,
			ConstName:   ev.Name + "Contract",
			Contract:    ev.Contract,
			PayloadType: payload,
			Validate:    validateFunc(ev, r.Proj, payload),
			Doc:         ev.Doc,
		})
	}
	if len(data.Events) == 0 {
		return nil
	}
	data.Imports = imports.sorted()
	return writeRendered(dir, "events.go", "events.tmpl", data)
}

// validateFunc renders the validation NewEvent is given: the payload
// type's generated method value, or `nil` when the type carries none.
// [pkgValidates] is the same condition validate.go is emitted on, so the
// descriptor and the file declaring the method cannot disagree.
func validateFunc(ev semantic.ResolvedEvent, proj *semantic.Project, payloadType string) string {
	home := proj.Packages[ev.PayloadPkg]
	if ev.Payload == nil || home == nil || !pkgValidates(home) {
		return "nil"
	}
	return "(*" + payloadType + ").Validate"
}

// writeRendered renders tmplName into dir/name, overwriting any existing
// file.
func writeRendered(dir, name, tmplName string, data any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl(tmplName), data)
	if err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	return os.WriteFile(filepath.Join(dir, name), formatted, 0o644)
}
