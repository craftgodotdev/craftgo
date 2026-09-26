package golang

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// eventsData is the template input for one package's events.go.
type eventsData struct {
	Package    string
	ImportDecl string
	Events     []eventDescriptor
}

// eventDescriptor is one event contract and the payload type its descriptor carries.
type eventDescriptor struct {
	Name      string
	ConstName string
	Contract  string
	// PayloadType is the payload's Go type, a slice for a `payload T[]` contract.
	PayloadType string
	// Validate is the validator NewEvent receives (see validateFunc).
	Validate string
	// ValidateElems declares Validate as a loop over an array payload's elements.
	ValidateElems bool
	// Doc heads the descriptor's doc comment ([docHead]).
	Doc []string
}

// generatePackageEvents writes outDir/<package>/events.go, a contract constant and a descriptor
// per event; a package without events gets no file.
func generatePackageEvents(pkg *semantic.Package, cfg *config.Config, projectRoot, outDir string, r *projectResolver) error {
	r = resolverFor(pkg, r)
	imports := newImportSet(r.Module, r, goImport{Alias: localAlias, Path: outputsOf(cfg).types.sub(pkg.Name).pkg}, eventsNames)
	imports.fixed("craftevents", eventsRuntimeImport)
	data := eventsData{Package: pkg.Name}
	for _, name := range slices.Sorted(maps.Keys(pkg.Events)) {
		ev, ok := r.Project().LookupEvent(pkg.Name, name)
		if !ok {
			continue
		}
		payload := imports.named(ev.PayloadRef)
		if ev.PayloadArray {
			payload = "[]" + payload
		}
		validate := validateFunc(ev, r.Project(), payload)
		elems := ev.PayloadArray && validate != "nil"
		if elems {
			imports.use("fmt")
		}
		data.Events = append(data.Events, eventDescriptor{
			Name:          ev.Name,
			ConstName:     idents.EventContractName(ev.Name),
			Contract:      ev.Contract,
			PayloadType:   payload,
			Validate:      validate,
			ValidateElems: elems,
			Doc:           docHead(ev.Doc),
		})
	}
	if len(data.Events) == 0 {
		return nil
	}
	data.ImportDecl = imports.decl()
	return writeGo(filepath.Join(projectRoot, outDir, pkg.Name, "events.go"), tmpl("events.tmpl"), data)
}

// validateFunc renders NewEvent's validator: the payload's Validate method value, the file's
// element validator for a `payload T[]` contract, or nil when the payload's package has no validate.go.
func validateFunc(ev semantic.ResolvedEvent, proj *semantic.Project, payloadType string) string {
	home := proj.Packages[ev.PayloadPkg]
	if ev.Payload == nil || home == nil || !pkgValidates(home) {
		return "nil"
	}
	if ev.PayloadArray {
		return "validate" + ev.Name
	}
	return "(*" + payloadType + ").Validate"
}
