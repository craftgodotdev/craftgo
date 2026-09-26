package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Generate writes the Go output of proj under projectRoot: the types and pb code, then, unless the
// project is contracts-only, the application layer. protos is nil when the design has no proto.
func Generate(proj *semantic.Project, protos *protodesign.Set, cfg *config.Config, projectRoot string) error {
	names := proj.PackageNames()
	resolvers := make(map[string]*projectResolver, len(names))
	for _, name := range names {
		resolvers[name] = buildProjectResolver(proj, cfg, name)
	}
	typesDir := outputsOf(cfg).types.at(projectRoot)
	fills := fillSetOf(proj)
	for _, name := range names {
		p, r := proj.Packages[name], resolvers[name]
		if err := runSteps(name, []genStep{
			{"types", func() error { return generateTypes(p, typesDir, r) }},
			{"enums", func() error { return generateEnums(p, typesDir) }},
			{"errors", func() error { return generateErrorsFilling(p, typesDir, r, fills) }},
			{"validators", func() error { return generateValidators(p, typesDir, r) }},
			{"fill", func() error { return generateFill(p, typesDir, fills) }},
		}); err != nil {
			return err
		}
	}
	// The plugins write nothing when output.pb is "-".
	if protos != nil {
		if err := runSteps("proto", []genStep{
			{"pb", func() error { return protodesign.RunPlugins(protos, projectRoot) }},
		}); err != nil {
			return err
		}
	}
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
		{"wiring-grpc", func() error { return generateWiringGRPC(protos, cfg, projectRoot) }},
		{"config", func() error { return generateRuntimeConfig(proj, protos, cfg, projectRoot) }},
		{"svccontext", func() error { return generateSvccontext(cfg, projectRoot) }},
		{"main", func() error { return generateProjectMain(proj, protos, cfg, projectRoot) }},
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
