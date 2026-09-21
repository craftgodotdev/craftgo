package golang

import (
	"fmt"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Generate runs the Go pipeline for proj under projectRoot: the
// pre-flight checks that reject a design before any file is written,
// then per package the type artefacts (types, enums, errors,
// validators), the middleware scaffolds, per package the service
// artefacts (transport, service stubs, routes), per proto service the
// gRPC server package and logic stubs, and finally the project-wide
// files (routes umbrella, runtime scaffolds, main.go).
//
// protos is the compiled proto set, nil when the design holds none.
//
// The design is validated, and the event target and the OpenAPI
// projection run, around it; see [codegen.Generate].
func Generate(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	names := sortedPackageNames(proj)
	resolvers := make(map[string]*projectResolver, len(names))
	for _, name := range names {
		resolvers[name] = buildProjectResolver(proj, cfg, name)
	}
	typesDir := filepath.Join(projectRoot, cfg.Output.Types)
	for _, name := range names {
		p, r := proj.Packages[name], resolvers[name]
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
	for _, name := range names {
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
	if protos.HasServices() {
		if err := runSteps("grpc", []genStep{
			{"server", func() error { return generateGRPCServers(protos, cfg, projectRoot) }},
			{"service", func() error { return generateGRPCServices(protos, cfg, projectRoot) }},
		}); err != nil {
			return err
		}
	}
	return runSteps("", []genStep{
		{"routes-umbrella", func() error { return generateProjectRoutesUmbrella(proj, cfg, projectRoot) }},
		{"wiring", func() error { return generateWiring(proj, cfg, projectRoot) }},
		{"config", func() error { return generateRuntimeConfig(cfg, projectRoot) }},
		{"svccontext", func() error { return generateSvccontext(proj, cfg, projectRoot) }},
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
