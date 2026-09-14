package golang

import (
	"fmt"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Generate runs the Go pipeline for proj under projectRoot: the
// pre-flight checks that reject a design before any file is written,
// then per package the type artefacts (types, enums, errors,
// validators), the middleware scaffolds, per package the service
// artefacts (transport, service stubs, routes), and finally the
// project-wide files (routes umbrella, runtime scaffolds, main.go).
//
// proj is what this project deploys; the type artefacts come from the
// whole design it projects, under the root the contract half resolves
// against. The two are the same project and the same root unless the
// manifest names a design source.
//
// The design is validated, and the event targets and the document
// projections run, around it; see [codegen.Generate].
func Generate(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	design := proj.Design()
	designNames := sortedPackageNames(design)
	resolvers := make(map[string]*projectResolver, len(designNames))
	for _, name := range designNames {
		resolvers[name] = buildProjectResolver(design, cfg, name)
	}
	typesDir := filepath.Join(cfg.LibraryRoot(projectRoot), cfg.Output.Types)
	for _, name := range designNames {
		p, r := design.Packages[name], resolvers[name]
		if err := runSteps(name, []genStep{
			{"types", func() error { return generateTypes(p, typesDir, r) }},
			{"enums", func() error { return generateEnums(p, typesDir) }},
			{"errors", func() error { return generateErrors(p, typesDir, r) }},
			{"validators", func() error { return generateValidators(p, typesDir, r) }},
		}); err != nil {
			return err
		}
	}
	// A contracts project stops here: the rest is the application half,
	// which the deployables that import this one generate for themselves.
	if cfg.Output.ContractsOnly() {
		return nil
	}
	if err := generateProjectMiddlewares(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("middlewares: %w", err)
	}
	for _, name := range sortedPackageNames(proj) {
		p, r := proj.Packages[name], resolvers[name]
		if len(p.Services) == 0 {
			continue
		}
		if err := runSteps(name, []genStep{
			{"transport", func() error { return generateTransport(p, cfg, projectRoot, r) }},
			{"service", func() error { return generateService(p, cfg, projectRoot, r) }},
			{"routes-svc", func() error { return generateRoutes(p, cfg, projectRoot) }},
		}); err != nil {
			return err
		}
	}
	return runSteps("", []genStep{
		{"routes-umbrella", func() error { return generateProjectRoutesUmbrella(proj, cfg, projectRoot) }},
		{"wiring", func() error { return generateWiring(proj, cfg, projectRoot) }},
		{"config", func() error { return generateRuntimeConfig(cfg, projectRoot) }},
		{"svccontext", func() error { return generateSvccontext(proj, cfg, projectRoot) }},
		{"svccontext-events", func() error { return generateSvccontextEvents(proj, cfg, projectRoot) }},
		{"main", func() error { return generateProjectMain(proj, cfg, projectRoot) }},
	})
}

// genStep pairs a codegen call with the label used to wrap its error.
type genStep struct {
	label string
	fn    func() error
}

// runSteps runs each step in order, wrapping the first failure with its
// label and the package name when one is given.
func runSteps(pkgName string, steps []genStep) error {
	for _, s := range steps {
		if err := s.fn(); err != nil {
			if pkgName != "" {
				return fmt.Errorf("%s(%s): %w", s.label, pkgName, err)
			}
			return fmt.Errorf("%s: %w", s.label, err)
		}
	}
	return nil
}
