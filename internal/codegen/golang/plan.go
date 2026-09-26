package golang

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Plan lists what [Generate] regenerates under projectRoot: each path the sweep covers, a
// directory it regenerates into or a single file, with the headers of the files written there,
// and the files a run writes. A gen-once output (output.service, output.middleware,
// output.config, main.go) is in neither, so the sweep keeps it.
func Plan(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) (paths map[string][]string, files []string) {
	paths = map[string][]string{}
	contracts := cfg.Output.ContractsOnly()
	for _, k := range outputsOf(cfg).keys() {
		if !k.regenerated || (contracts && k.application) {
			continue
		}
		if len(k.files) == 0 {
			dir := k.dir.at(projectRoot)
			paths[dir] = append(paths[dir], GeneratedHeader)
		}
		for _, name := range k.files {
			file := k.dir.at(projectRoot, name)
			paths[file] = append(paths[file], GeneratedHeader)
		}
	}
	for _, file := range protos.OwnedPBFiles(projectRoot) {
		paths[file] = protodesign.PluginHeaders
	}
	return paths, regeneratedFiles(proj, protos, cfg, projectRoot)
}

// regeneratedFiles names every file [Generate] rewrites.
func regeneratedFiles(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string {
	out := outputsOf(cfg)
	fills := newFillSet(proj)
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
		if fills.pkgFills(pkg) {
			files = append(files, dir.at(projectRoot, "fill.go"))
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
	files = append(files, out.wiring.at(projectRoot, wiringFile))
	if protos.HasServices() {
		files = append(files, out.wiring.at(projectRoot, wiringGRPCFile))
	}
	return append(files, out.svccontext.at(projectRoot, middlewaresFile))
}

// EventPlan is [Plan] for [GenerateEventTarget] writing under outDir: one events.go per DSL
// package that declares an event.
func EventPlan(proj *semantic.Project, projectRoot, outDir string) (paths map[string][]string, files []string) {
	root := filepath.Join(projectRoot, outDir)
	return map[string][]string{root: {GeneratedHeader}}, slices.Collect(maps.Keys(expectedEventFiles(proj, root)))
}
