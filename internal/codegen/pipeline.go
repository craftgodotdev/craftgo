package codegen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// Generate runs the whole codegen pipeline for proj under projectRoot:
// the pre-flight checks that reject a design before any file is written,
// then per package the type artefacts (types, enums, errors, validators),
// the middleware scaffolds, per package the service artefacts (transport,
// service stubs, routes), and finally the project-wide files (routes
// umbrella, runtime scaffolds, main.go, openapi.yaml).
func Generate(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	pkgNames := sortedPackageNames(proj)
	if err := validateProject(proj, cfg, pkgNames); err != nil {
		return err
	}
	resolvers := make(map[string]*ProjectResolver, len(pkgNames))
	for _, name := range pkgNames {
		resolvers[name] = BuildProjectResolver(proj, cfg, name)
	}
	typesDir := filepath.Join(projectRoot, cfg.Output.Types)
	for _, name := range pkgNames {
		p, r := proj.Packages[name], resolvers[name]
		if err := runSteps(name, []genStep{
			{"types", func() error { return GenerateTypes(p, typesDir, r) }},
			{"enums", func() error { return GenerateEnums(p, typesDir) }},
			{"errors", func() error { return GenerateErrors(p, typesDir, r) }},
			{"validators", func() error { return GenerateValidators(p, typesDir, r) }},
		}); err != nil {
			return err
		}
	}
	if err := GenerateProjectMiddlewares(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("middlewares: %w", err)
	}
	for _, name := range pkgNames {
		p, r := proj.Packages[name], resolvers[name]
		if len(p.Services) == 0 {
			continue
		}
		if err := runSteps(name, []genStep{
			{"transport", func() error { return GenerateTransport(p, cfg, projectRoot, r) }},
			{"service", func() error { return GenerateService(p, cfg, projectRoot, r) }},
			{"routes-svc", func() error { return GenerateRoutes(p, cfg, projectRoot) }},
		}); err != nil {
			return err
		}
	}
	return runSteps("", []genStep{
		{"routes-umbrella", func() error { return GenerateProjectRoutesUmbrella(proj, cfg, projectRoot) }},
		{"config", func() error { return GenerateRuntimeConfig(cfg, projectRoot) }},
		{"svccontext", func() error { return GenerateSvccontext(cfg, projectRoot) }},
		{"main", func() error { return GenerateProjectMain(proj, cfg, projectRoot) }},
		{"openapi", func() error { return GenerateProjectOpenAPI(proj, cfg, projectRoot) }},
	})
}

// validateProject runs the checks that must reject a design before any
// file is written: unknown security schemes, route patterns net/http's
// ServeMux would refuse to register together, and operationId /
// component-schema name collisions.
func validateProject(proj *semantic.Project, cfg *config.Config, pkgNames []string) error {
	for _, name := range pkgNames {
		p := proj.Packages[name]
		if p == nil {
			continue
		}
		if errs := ValidateSecurityRefs(p, cfg); len(errs) > 0 {
			return fmt.Errorf("security scheme errors in package %s:\n  %s", name, strings.Join(errs, "\n  "))
		}
	}
	if msgs := ValidateRouteConflicts(proj, cfg); len(msgs) > 0 {
		return fmt.Errorf("conflicting routes:\n  %s", strings.Join(msgs, "\n  "))
	}
	return ValidateProjectOpenAPI(proj, cfg)
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

// sortedPackageNames returns the project's non-blank package names in
// alphabetical order so every per-package phase emits in a stable order.
func sortedPackageNames(proj *semantic.Project) []string {
	out := make([]string, 0, len(proj.Packages))
	for k := range proj.Packages {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
