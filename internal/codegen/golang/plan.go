package golang

import (
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// RegeneratedFiles names every file [Generate] rewrites. The sweep deletes any other generated file
// under [OutputDirs], so an emitted path missing here is removed after it is written.
func RegeneratedFiles(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) []string {
	typesRoot := filepath.Join(projectRoot, cfg.Output.Types)
	var files []string
	for _, name := range sortedPackageNames(proj) {
		pkg := proj.Packages[name]
		if pkg == nil {
			continue
		}
		if pkgDeclaresTypes(pkg) {
			files = append(files, filepath.Join(typesRoot, name, "types.go"))
		}
		if pkgValidates(pkg) {
			files = append(files, filepath.Join(typesRoot, name, "validate.go"))
		}
		if len(pkg.Enums) > 0 {
			files = append(files, filepath.Join(typesRoot, name, "enums.go"))
		}
		if len(pkg.Errors) > 0 {
			files = append(files, filepath.Join(typesRoot, name, "errors.go"))
		}
	}
	files = append(files, protos.PBFiles(projectRoot)...)
	if cfg.Output.ContractsOnly() {
		return files
	}
	routesRoot := filepath.Join(projectRoot, cfg.Output.Routes)
	var routes []string
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
				files = append(files, filepath.Join(dir, idents.FileName(m.Name, cfg.Output.FileCase)+".go"))
			}
		}
		for _, seg := range sortedKeys(routeSegments(pkg, cfg)) {
			routes = append(routes, filepath.Join(routesRoot, filepath.FromSlash(seg), "routes.go"))
		}
	}
	// The umbrella routes.go exists while some route does.
	if len(routes) > 0 {
		routes = append(routes, filepath.Join(routesRoot, "routes.go"))
	}
	files = append(files, routes...)
	if protos != nil {
		for _, svc := range protos.Services {
			dir := grpcServerDir(projectRoot, cfg, svc)
			files = append(files, filepath.Join(dir, grpcServerFile+".go"))
			for _, m := range svc.Methods {
				files = append(files, filepath.Join(dir, m.File+".go"))
			}
		}
	}
	files = append(files, filepath.Join(projectRoot, cfg.Output.Wiring, "wiring.go"))
	if protos.HasServices() {
		files = append(files, filepath.Join(projectRoot, cfg.Output.Wiring, "grpc.go"))
	}
	return append(files, filepath.Join(projectRoot, fileDirRel(cfg.Output.Svccontext), "middlewares.go"))
}

// OutputDirs names the directories [Generate] regenerates into; the gen-once outputs
// (output.service, output.middleware, output.config, main.go) are not among them.
func OutputDirs(cfg *config.Config, projectRoot string) []string {
	dirs := []string{filepath.Join(projectRoot, cfg.Output.Types)}
	if cfg.Output.ContractsOnly() {
		return dirs
	}
	return append(dirs,
		filepath.Join(projectRoot, cfg.Output.Transport),
		filepath.Join(projectRoot, cfg.Output.Routes),
		filepath.Join(projectRoot, cfg.Output.GRPC),
		filepath.Join(projectRoot, cfg.Output.Wiring),
		filepath.Join(projectRoot, fileDirRel(cfg.Output.Svccontext)),
	)
}

// RegeneratedEventFiles names what [GenerateEventTarget] writes under
// outDir: one events.go per DSL package that declares an event.
func RegeneratedEventFiles(proj *semantic.Project, projectRoot, outDir string) []string {
	root := filepath.Join(projectRoot, outDir)
	var files []string
	for path := range expectedEventFiles(proj, root) {
		files = append(files, path)
	}
	return files
}
