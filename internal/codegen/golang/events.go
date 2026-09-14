package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The Go event library, one directory per DSL package under the event
// target's output:
//
//	<events>/<package>/events.go    one descriptor per contract
//	<events>/<package>/handlers.go  one handler interface per consuming service
//
// Both are pure contract: they import the event runtime and the payload
// types and nothing else, so one library serves every deployable that
// imports it. Everything about DELIVERY - which group, which middleware,
// which process runs what - is the application's, and reaches the
// generated Register call as an argument.

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

// handlersData is the template input for one package's handlers.go.
type handlersData struct {
	Package  string
	Imports  []extraImport
	Services []handlerService
}

// handlerService is one consuming service: the interface the application
// implements, the groups it supplies, and the Register call binding them.
type handlerService struct {
	Name     string
	Doc      []string
	Consumes []handlerConsume
	// MissingGroups is the condition under which a consume would be left
	// without a group - every field empty, with no Default to fall back
	// to.
	MissingGroups string
}

// handlerConsume is one `consume` block: a method on the interface, a
// field on the Groups struct, and one bus.Register call.
type handlerConsume struct {
	Name       string
	QuotedName string
	// Descriptor names the event's descriptor as this file sees it -
	// bare for a contract this package declares, qualified when the
	// consume reaches into another package's library.
	Descriptor  string
	PayloadType string
	Doc         []string
}

// generatePackageEvents writes pkg's event library under the Go event
// target's directory: the descriptors, and the handler interfaces of
// every service that consumes one. A package that declares no event and
// consumes none leaves no directory behind.
func generatePackageEvents(pkg *semantic.Package, cfg *config.Config, projectRoot, outDir string, r *projectResolver) error {
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	r = resolverFor(pkg, r)
	dir := filepath.Join(projectRoot, outDir, pkg.Name)
	if err := writePackageDescriptors(pkg, cfg, dir, r); err != nil {
		return err
	}
	return writePackageHandlers(pkg, cfg, outDir, dir, r)
}

// writePackageDescriptors emits events.go: one contract constant and one
// descriptor per event the package declares, wherever it was written.
func writePackageDescriptors(pkg *semantic.Package, cfg *config.Config, dir string, r *projectResolver) error {
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

// writePackageHandlers emits handlers.go: the handler interface, the
// group set and the Register call of every service in pkg that consumes
// a contract. eventsOut is the target's own directory, which a consume
// reaching into another package's library imports.
func writePackageHandlers(pkg *semantic.Package, cfg *config.Config, eventsOut, dir string, r *projectResolver) error {
	imports := newImportSet(r.CrossPkg)
	typesImport := typesImportRoot(cfg) + "/" + pkg.Name
	data := handlersData{Package: pkg.Name}
	for _, svcName := range sortedServices(pkg) {
		svc := pkg.Services[svcName]
		hs := handlerService{Name: svcName}
		if svc.Primary != nil {
			hs.Doc = svc.Primary.Doc
		}
		for _, cd := range svc.Consumers {
			rc := r.Proj.ResolveConsumer(pkg, svcName, cd)
			if rc.Event.Contract == "" {
				continue
			}
			hs.Consumes = append(hs.Consumes, handlerConsume{
				Name:        rc.Name,
				QuotedName:  strconv.Quote(rc.Name),
				Descriptor:  descriptorRef(pkg.Name, rc.Event, cfg, eventsOut, imports),
				PayloadType: imports.payloadType(rc.Event, typesImport, cfg),
				Doc:         rc.Doc,
			})
		}
		if len(hs.Consumes) == 0 {
			continue
		}
		hs.MissingGroups = missingGroupsCheck(hs.Consumes)
		data.Services = append(data.Services, hs)
	}
	if len(data.Services) == 0 {
		return nil
	}
	data.Imports = imports.sorted()
	return writeRendered(dir, "handlers.go", "handlers.tmpl", data)
}

// descriptorRef names the descriptor of the contract a consume handles.
// A contract this package declares is a value in this very file's
// package; one declared elsewhere comes from that package's library,
// which is imported under an alias of its own so it never collides with
// the payload types of the same name.
func descriptorRef(homePkg string, ev semantic.ResolvedEvent, cfg *config.Config, eventsOut string, imports *importSet) string {
	if ev.Package == homePkg {
		return ev.Name
	}
	path := goImportFromRel(cfg.Package, eventsOut) + "/" + ev.Package
	imports.add(extraImport{Alias: escapeReserved(strings.ToLower(ev.Package) + "events"), Path: path})
	return imports.aliasFor(path) + "." + ev.Name
}

// missingGroupsCheck renders the condition that leaves a consume without
// a group: its own field empty. The caller ANDs it with an empty
// Default, so the whole check fires once, for the service, rather than
// per consume.
func missingGroupsCheck(consumes []handlerConsume) string {
	parts := make([]string, len(consumes))
	for i, c := range consumes {
		parts[i] = "groups." + c.Name + ` == ""`
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " || ") + ")"
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

// escapeReserved renames an alias that collides with an identifier the
// event templates already bind, the way [importSet.aliasFor] does for
// payload packages. A package named `Craft` would otherwise import its
// library under `craftevents`, which is the alias the runtime holds.
func escapeReserved(alias string) string {
	candidate := alias
	for i := 2; reservedAliases[candidate]; i++ {
		candidate = fmt.Sprintf("%s%d", alias, i)
	}
	return candidate
}
