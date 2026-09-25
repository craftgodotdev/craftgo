package golang

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// RegeneratedFiles names every file [Generate] rewrites. The sweep deletes any other generated file
// under [OutputDirs], so an emitted path missing here is removed after it is written.
func RegeneratedFiles(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string {
	out := outputsOf(cfg)
	var files []string
	for _, name := range proj.PackageNames() {
		pkg := proj.Packages[name]
		if pkg == nil {
			continue
		}
		dir := out.types.sub(name)
		if pkgDeclaresTypes(pkg) {
			files = append(files, dir.at(projectRoot, "types.go"))
		}
		if pkgValidates(pkg) {
			files = append(files, dir.at(projectRoot, "validate.go"))
		}
		if len(pkg.Enums) > 0 {
			files = append(files, dir.at(projectRoot, "enums.go"))
		}
		if len(pkg.Errors) > 0 {
			files = append(files, dir.at(projectRoot, "errors.go"))
		}
	}
	files = append(files, protos.PBFiles(projectRoot)...)
	if cfg.Output.ContractsOnly() {
		return files
	}
	routes := map[string]bool{}
	for s := range projectSegments(proj, cfg.Output.FileCase) {
		for m := range s.methods() {
			files = append(files, out.transport.sub(s.dir).at(projectRoot, methodFile(m, cfg.Output.FileCase)))
		}
		routes[out.routes.sub(s.dir).at(projectRoot, "routes.go")] = true
	}
	// The umbrella routes.go exists while some route does.
	if len(routes) > 0 {
		files = append(files, slices.Sorted(maps.Keys(routes))...)
		files = append(files, out.routes.at(projectRoot, "routes.go"))
	}
	if protos != nil {
		for _, svc := range protos.Services {
			dir := out.grpc.sub(svc.Dir)
			files = append(files, dir.at(projectRoot, grpcServerFile+".go"))
			for _, m := range svc.Methods {
				files = append(files, dir.at(projectRoot, m.File+".go"))
			}
		}
	}
	files = append(files, out.wiring.at(projectRoot, "wiring.go"))
	if protos.HasServices() {
		files = append(files, out.wiring.at(projectRoot, "grpc.go"))
	}
	return append(files, out.svccontext.at(projectRoot, "middlewares.go"))
}

// OutputDirs names the directories [Generate] regenerates into; the gen-once outputs
// (output.service, output.middleware, output.config, main.go) are not among them.
func OutputDirs(cfg *config.Config, projectRoot string) []string {
	contracts := cfg.Output.ContractsOnly()
	var dirs []string
	for _, k := range outputsOf(cfg).keys() {
		if k.regenerated && (!contracts || !k.application) {
			dirs = append(dirs, k.dir.at(projectRoot))
		}
	}
	return dirs
}

// RegeneratedEventFiles names what [GenerateEventTarget] writes under
// outDir: one events.go per DSL package that declares an event.
func RegeneratedEventFiles(proj *semantic.Project, projectRoot, outDir string) []string {
	return slices.Collect(maps.Keys(expectedEventFiles(proj, filepath.Join(projectRoot, outDir))))
}
