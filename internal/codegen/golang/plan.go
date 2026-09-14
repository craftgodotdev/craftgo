package golang

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/claim"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// The files this package REGENERATES, named before the run writes them,
// so two designs claiming one file are told apart before either is
// touched. Gen-once scaffolds - the logic stubs, the middleware impls,
// config, main.go, svccontext.go - are absent on purpose: they are
// written only when missing, so no second manifest can clobber one.
//
// The codegen package regenerates a design and compares the result against
// this plan, so an emitter that gains or moves a file fails a test rather
// than silently losing its claim.

// PlannedOutputs names what [Generate] writes, grouped by the directory
// each claim is filed in.
func PlannedOutputs(proj *semantic.Project, cfg *config.Config, projectRoot string) []claim.Output {
	typesRoot := filepath.Join(projectRoot, cfg.Output.Types)
	types := claim.Output{Root: typesRoot}
	for _, name := range sortedPackageNames(proj) {
		pkg := proj.Packages[name]
		if pkg == nil {
			continue
		}
		if pkgDeclaresTypes(pkg) {
			types.Files = append(types.Files, filepath.Join(typesRoot, name, "types.go"))
		}
		if pkgValidates(pkg) {
			types.Files = append(types.Files, filepath.Join(typesRoot, name, "validate.go"))
		}
		if len(pkg.Enums) > 0 {
			types.Files = append(types.Files, filepath.Join(typesRoot, name, "enums.go"))
		}
		if len(pkg.Errors) > 0 {
			types.Files = append(types.Files, filepath.Join(typesRoot, name, "errors.go"))
		}
	}
	if cfg.Output.ContractsOnly() {
		return []claim.Output{types}
	}
	transport := claim.Output{Root: filepath.Join(projectRoot, cfg.Output.Transport)}
	routes := claim.Output{Root: filepath.Join(projectRoot, cfg.Output.Routes)}
	for _, name := range sortedPackageNames(proj) {
		pkg := proj.Packages[name]
		if pkg == nil || len(pkg.Services) == 0 {
			continue
		}
		for _, svcName := range sortedServices(pkg) {
			svc := pkg.Services[svcName]
			groups := methodGroups(svc)
			for _, m := range svc.Methods {
				dir := serviceOutputDir(projectRoot, cfg.Output.Transport, svcName, groups[m.Name], cfg.Output.FileCase)
				transport.Files = append(transport.Files, filepath.Join(dir, idents.FileName(m.Name, cfg.Output.FileCase)+".go"))
			}
		}
		for _, seg := range sortedKeys(routeSegments(pkg, cfg)) {
			routes.Files = append(routes.Files, filepath.Join(routes.Root, filepath.FromSlash(seg), "routes.go"))
		}
	}
	// The umbrella exists for as long as one route does; with none it is
	// removed rather than written.
	if len(routes.Files) > 0 {
		routes.Files = append(routes.Files, filepath.Join(routes.Root, "routes.go"))
	}
	container := filepath.Join(projectRoot, fileDirRel(cfg.Output.Svccontext))
	return []claim.Output{
		types,
		transport,
		routes,
		{Root: filepath.Join(projectRoot, cfg.Output.Wiring), Files: []string{
			filepath.Join(projectRoot, cfg.Output.Wiring, "wiring.go"),
		}},
		{Root: container, Files: []string{
			filepath.Join(container, "middlewares.go"),
		}},
	}
}

// PlannedEventOutputs names what [GenerateEventTarget] writes: one
// library per DSL package under the target's own directory.
func PlannedEventOutputs(proj *semantic.Project, cfg *config.Config, projectRoot, outDir string) []claim.Output {
	events := claim.Output{Root: filepath.Join(projectRoot, outDir)}
	for path := range expectedEventFiles(proj, events.Root) {
		events.Files = append(events.Files, path)
	}
	return []claim.Output{events}
}
