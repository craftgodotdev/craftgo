// Command craftgo is the CLI entrypoint that drives the design-first
// pipeline: locate the project manifest, parse every `.craftgo` source
// file, run semantic analysis, and dispatch each codegen artefact.
//
// Usage:
//
//	craftgo init [path] [-package <module>]
//	craftgo gen  [-f <design-folder>] [-c|--context <project-root>] [path]
//
// `init` scaffolds a fresh design folder at <path> (default `design`).
// The path argument IS the design folder - manifest + sample `.craftgo`
// files land flat inside it. Existing files are never overwritten so
// re-running on a populated directory fills only the gaps.
//
// `gen` resolves the design folder one of two ways: with `-f` it uses
// the supplied path directly; without it walks upward from <path> (or
// cwd) looking for a craftgo.design.yaml, probing direct subdirs of
// any name at each level. The project root the `output:` paths
// resolve against is `-c <root>` when given, else cwd in the `-f`
// flow, else the parent of the manifest folder (legacy compat).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/codegen"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// version is the CLI's reported version. Kept as a build-time constant for
// now; release tooling can override via `-ldflags="-X main.version=..."`.
const version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = runGen(os.Args[2:])
	case "init":
		err = runInit(os.Args[2:])
	case "fmt":
		err = runFmt(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "help", "--help", "-h":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "craftgo: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err == nil {
		return
	}
	// `-h` / `--help` returns this sentinel - flag package already
	// printed the per-subcommand usage; exit 0 without piling on
	// our "craftgo: …" prefix.
	if errors.Is(err, errHelpRequested) {
		return
	}
	fmt.Fprintln(os.Stderr, "craftgo: "+err.Error())
	os.Exit(1)
}

// usage prints a short command summary to stdout. Verbose enough to remind
// returning users of the positional-path convention but not so detailed that
// it becomes a maintenance burden - full docs live in the README.
func usage() {
	fmt.Println(`craftgo - design-first Go API framework

Usage:
  craftgo init [path]
                          Scaffold a design folder at <path> (default: 'design').
                          The supplied path IS the design folder - the manifest
                          (craftgo.design.yaml) lands flat inside it. The Go
                          module path is read from go.mod at gen time, so init
                          itself does not need a -package flag.

  craftgo gen [-f <design-folder>] [-c|--context <project-root>] [path]
                          Generate types, handlers, routes, OpenAPI from
                          .craftgo files. Flags:
                            -f, --folder   path to the folder holding
                                           craftgo.design.yaml (skips walk-up)
                            -c, --context  project root the output: paths
                                           resolve against (defaults to cwd
                                           when -f is given, otherwise to
                                           the parent of the manifest dir)
                          Without -f, walks upward from <path> (or cwd) for
                          craftgo.design.yaml, probing direct subdirs (any
                          name) at each level. The Go module path is read
                          from go.mod, walking up from the project root -
                          run "go mod init <module>" first if it does not
                          exist yet.

  craftgo fmt [path] [-l] [-w]
                          Canonical-format .craftgo files (default: write back)
  craftgo version         Print the CLI version
  craftgo help            Show this message

For 'fmt', path may be a single file or a directory (recursed for *.craftgo).`)
}

// parseGenArgs extracts the three controls `craftgo gen` honours:
//
//   - `-f <folder>` / `--folder`: explicit design folder (where
//     craftgo.design.yaml lives). Skips the walk-up search.
//   - `-c <root>` / `--context`: project root the `output:` paths
//     resolve against. Defaults to cwd when -f is given, otherwise
//     to the parent of the manifest folder (legacy).
//   - positional path: walk-up start (legacy compat). Defaults to
//     `.` when neither -f nor a positional is given.
//
// Implementation goes through [flag.NewFlagSet] so users get free
// `-h` / `--help` text and consistent `--flag=value` parsing -
// matching how `craftgo fmt` and the rest of the Go CLI ecosystem
// handle subcommand flags. Both short and long names share the
// same destination so editors / shell completions surface either
// spelling.
//
// Returns (manifestFolder, contextRoot, positionalTarget, error).
// Exactly one of manifestFolder / positionalTarget is meaningful
// at the call site - the runGen dispatch picks the path based on
// which is set.
func parseGenArgs(args []string) (manifest, ctxRoot, positional string, err error) {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	fs.StringVar(&manifest, "f", "", "design folder holding craftgo.design.yaml (skips walk-up)")
	fs.StringVar(&manifest, "folder", "", "alias for -f")
	fs.StringVar(&ctxRoot, "c", "", "project root the output paths resolve against (defaults to cwd when -f is given)")
	fs.StringVar(&ctxRoot, "context", "", "alias for -c")
	if perr := fs.Parse(args); perr != nil {
		// flag.ErrHelp is the explicit user request for `-h`/`--help`;
		// surface a sentinel error the caller recognises as
		// "successful early exit, no usage error".
		return "", "", "", parseFlagError("gen", perr)
	}
	rest := fs.Args()
	switch len(rest) {
	case 0:
		positional = "."
	case 1:
		positional = rest[0]
	default:
		return "", "", "", fmt.Errorf("gen: too many positional arguments (got %d, want at most 1)", len(rest))
	}
	return manifest, ctxRoot, positional, nil
}

// parseFlagError translates a [flag.ContinueOnError] result into the
// project's error contract: `-h` / `--help` is a clean exit with no
// noisy "flag: help requested" wrapper, every other parse error is
// prefixed with the subcommand name so the user sees `init: …`
// rather than the bare flag-package message.
func parseFlagError(subcommand string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, flag.ErrHelp) {
		// The flag package already printed Usage; signalling
		// "command exited cleanly" is the right shape for the
		// caller. Returning nil would silently fall through into
		// the rest of runGen / runInit, so use a sentinel that
		// short-circuits the dispatcher in main().
		return errHelpRequested
	}
	return fmt.Errorf("%s: %w", subcommand, err)
}

// errHelpRequested is the sentinel returned by [parseFlagError] when
// the user passed `-h`/`--help`. main() recognises it and exits 0
// without emitting "craftgo: …" prefix noise.
var errHelpRequested = errors.New("help requested")

// resolveGenPaths picks the design folder and project root from the
// parsed flags. Two flows:
//
//   - `-f <folder>` → design folder is exactly that path. Project
//     root defaults to cwd (the dir the user ran the command from)
//     so the monorepo layout - design at contracts/v1, code at repo
//     root - works without further flags. `-c` overrides.
//
//   - no `-f` → walk up from `target` until craftgo.design.yaml is
//     found (or in any direct subdir along the way). Project root
//     stays the parent of the manifest dir (legacy convention),
//     unless `-c` overrides.
//
// The cwd default in the `-f` flow and the parent-of-manifest default
// in the walk-up flow give predictable results for the most common
// invocations: `craftgo gen` from the project root, `craftgo gen -f`
// from anywhere.
func resolveGenPaths(manifestFolder, contextRoot, target string) (*config.Config, string, string, error) {
	if manifestFolder != "" {
		root := contextRoot
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, "", "", err
			}
			root = cwd
		}
		return config.FindAt(manifestFolder, root)
	}
	cfg, projectRoot, designDir, err := config.Find(target)
	if err != nil {
		return nil, "", "", err
	}
	if contextRoot != "" {
		absRoot, absErr := filepath.Abs(contextRoot)
		if absErr != nil {
			return nil, "", "", absErr
		}
		projectRoot = absRoot
	}
	return cfg, projectRoot, designDir, nil
}

// runGen wires the full design → codegen pipeline. The body reads as
// the high-level outline (resolve → analyze → validate → emit per
// concern → log) so a future reader can navigate phases without
// chasing nested loops. Each phase function is independently
// testable and contains the actual codegen calls; runGen itself
// only sequences them.
func runGen(args []string) error {
	manifestFolder, contextRoot, target, err := parseGenArgs(args)
	if err != nil {
		return err
	}
	cfg, projectRoot, designDir, err := resolveGenPaths(manifestFolder, contextRoot, target)
	if err != nil {
		return err
	}
	// Resolve the Go module path for the project root. ResolveModulePath
	// walks up looking for go.mod (so monorepo layouts with one shared
	// go.mod at the repo root and project root inside a sub-tree work
	// without further config) and computes the effective import-path
	// prefix every generated file consumes. We populate cfg.Package
	// here rather than reading it from the manifest so the manifest
	// can never drift from go.mod's truth.
	modulePath, err := config.ResolveModulePath(projectRoot)
	if err != nil {
		return err
	}
	cfg.Package = modulePath

	proj, err := analyzeDesign(designDir, cfg)
	if err != nil {
		return err
	}
	pkgNames := sortedPackageNames(proj)

	if err := validateSecurityRefs(proj, cfg, pkgNames); err != nil {
		return err
	}
	if err := genTypesPerPackage(proj, cfg, projectRoot, pkgNames); err != nil {
		return err
	}
	if err := genServicesPerPackage(proj, cfg, projectRoot, pkgNames); err != nil {
		return err
	}
	if err := genProjectArtefacts(proj, cfg, projectRoot); err != nil {
		return err
	}
	fmt.Printf("craftgo: generated %d package(s) under %s\n", len(proj.Packages), projectRoot)
	return nil
}

// analyzeDesign parses every `.craftgo` under designDir, runs the
// semantic analyser, and returns the validated [semantic.Project].
// Diagnostic-level errors collapse into a single multi-line error
// so callers don't have to thread the diagnostic slice further.
// A project with zero DSL packages is rejected here - the
// downstream codegen would silently produce nothing.
func analyzeDesign(designDir string, cfg *config.Config) (*semantic.Project, error) {
	files, err := parseDesign(designDir)
	if err != nil {
		return nil, err
	}
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{
		SecuritySchemes: securitySchemeNames(cfg),
		BasePath:        cfg.OpenAPI.BasePath,
		DesignRoot:      designDir,
	})
	if errs := formatSemanticErrors(diags); errs != "" {
		return nil, fmt.Errorf("%s", errs)
	}
	if len(proj.Packages) == 0 {
		return nil, fmt.Errorf("project has no DSL packages - every project must have at least one .craftgo file declaring `package X`")
	}
	return proj, nil
}

// sortedPackageNames returns the project's non-blank package names in
// alphabetical order. Used by every per-package gen phase so output
// files diff cleanly across runs regardless of the underlying map
// iteration order.
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

// validateSecurityRefs walks every package's `@security` references
// and surfaces any unresolved scheme as a single composite error.
// Multi-package projects can spread services across packages, so the
// validator runs over each one independently.
func validateSecurityRefs(proj *semantic.Project, cfg *config.Config, pkgNames []string) error {
	for _, name := range pkgNames {
		p := proj.Packages[name]
		if p == nil {
			continue
		}
		if errs := codegen.ValidateSecurityRefs(p, cfg); len(errs) > 0 {
			return fmt.Errorf("security scheme errors in package %s:\n  %s", name, strings.Join(errs, "\n  "))
		}
	}
	return nil
}

// genTypesPerPackage emits the four type-shape artefacts (types,
// enums, errors, validators) into <typesDir>/<pkgName>/ for every
// package. Cross-package field refs pick up Go imports through the
// per-call [codegen.BuildCrossPkg] context.
func genTypesPerPackage(proj *semantic.Project, cfg *config.Config, projectRoot string, pkgNames []string) error {
	typesDir := filepath.Join(projectRoot, cfg.Output.Types)
	for _, name := range pkgNames {
		p := proj.Packages[name]
		cross := codegen.BuildCrossPkg(proj, cfg, name)
		scalars := codegen.BuildScalarTable(proj, name)
		steps := []struct {
			label string
			fn    func() error
		}{
			{"types(" + name + ")", func() error { return codegen.GenerateTypesPackage(p, typesDir, cross) }},
			{"enums(" + name + ")", func() error { return codegen.GenerateEnums(p, typesDir) }},
			{"errors(" + name + ")", func() error { return codegen.GenerateErrorsPackage(p, typesDir, cross) }},
			{"validators(" + name + ")", func() error { return codegen.GenerateValidatorsWith(p, typesDir, cross, scalars) }},
		}
		for _, s := range steps {
			if err := s.fn(); err != nil {
				return fmt.Errorf("%s: %w", s.label, err)
			}
		}
	}
	return nil
}

// genServicesPerPackage emits service-shaped artefacts (handlers,
// helpers, logic, per-service routes) for every package that
// declares at least one service. Project-global middleware
// scaffolds run ONCE up-front so packages without services - like
// `shared` - still contribute their declarations to svccontext.
func genServicesPerPackage(proj *semantic.Project, cfg *config.Config, projectRoot string, pkgNames []string) error {
	if err := codegen.GenerateProjectMiddlewares(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("middlewares: %w", err)
	}
	for _, name := range pkgNames {
		p := proj.Packages[name]
		if len(p.Services) == 0 {
			continue
		}
		cross := codegen.BuildCrossPkg(proj, cfg, name)
		scalars := codegen.BuildScalarTable(proj, name)
		steps := []struct {
			label string
			fn    func() error
		}{
			{"transport(" + name + ")", func() error { return codegen.GenerateTransportWith(p, cfg, projectRoot, cross, scalars) }},
			{"transport-helpers(" + name + ")", func() error { return codegen.GenerateTransportHelpers(p, cfg, projectRoot) }},
			{"service(" + name + ")", func() error { return codegen.GenerateServicePackage(p, cfg, projectRoot, cross) }},
			{"routes-svc(" + name + ")", func() error { return codegen.GeneratePerServiceRoutes(p, cfg, projectRoot) }},
		}
		for _, s := range steps {
			if err := s.fn(); err != nil {
				return fmt.Errorf("%s: %w", s.label, err)
			}
		}
	}
	return nil
}

// genProjectArtefacts emits the project-wide artefacts in dependency
// order: routes-umbrella aggregates per-service routes, runtime
// scaffolds (config/, svccontext/) write the boot package, main.go
// stitches them together, and openapi.yaml is last so it sees the
// final symbol table. Runtime scaffolds self-skip when
// `output.main: "-"` opts the project out of the runtime layer.
func genProjectArtefacts(proj *semantic.Project, cfg *config.Config, projectRoot string) error {
	steps := []struct {
		label string
		fn    func() error
	}{
		{"routes-umbrella", func() error { return codegen.GenerateProjectRoutesUmbrella(proj, cfg, projectRoot) }},
		{"config", func() error { return codegen.GenerateRuntimeConfig(cfg, projectRoot) }},
		{"svccontext", func() error { return codegen.GenerateSvccontext(cfg, projectRoot) }},
		{"main", func() error { return codegen.GenerateProjectMain(proj, cfg, projectRoot) }},
		{"openapi", func() error { return codegen.GenerateProjectOpenAPI(proj, cfg, projectRoot) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.label, err)
		}
	}
	return nil
}

// runInit scaffolds a fresh design folder at args[0]. The path argument
// IS the design folder - `craftgo init contracts/v1` creates
// `contracts/v1/craftgo.design.yaml` directly inside that directory;
// no intermediate `design/` wrapper. When no path is supplied the
// default is `design` (creates a `design/` subdir of cwd) so a fresh
// `mkdir myapp && cd myapp && craftgo init` produces the conventional
// layout.
//
// The command refuses to overwrite an existing manifest so re-running
// on a populated folder is a silent no-op. There is no `-package`
// flag - the Go module path is read from `go.mod` at gen time, so
// the only manifest-side configuration is the optional output paths
// and OpenAPI metadata.
//
// init only owns the manifest scaffolding - the runtime artefacts
// (config/, svccontext/, main.go) are scaffolded by `craftgo gen`
// using the same gen-once policy so they live in one place
// (internal/codegen/templates/) and follow the same workflow as
// every other generated artefact.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	if perr := fs.Parse(args); perr != nil {
		return parseFlagError("init", perr)
	}
	rest := fs.Args()
	target := "design"
	switch len(rest) {
	case 0:
		// keep default
	case 1:
		target = rest[0]
	default:
		return fmt.Errorf("init: too many positional arguments (got %d, want at most 1)", len(rest))
	}

	designDir, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(designDir, 0o755); err != nil {
		return err
	}

	// Skip silently when the manifest already exists so re-running
	// init on a populated folder is a no-op.
	dest := filepath.Join(designDir, "craftgo.design.yaml")
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("craftgo: %s already exists, nothing to do\n", dest)
		return nil
	}
	if err := os.WriteFile(dest, []byte(initManifest()), 0o644); err != nil {
		return err
	}
	fmt.Printf("craftgo: wrote %s\n", dest)
	fmt.Println("next steps:")
	fmt.Printf("  1. ensure `go.mod` exists at your project root (`go mod init <module>`)\n")
	fmt.Printf("  2. add at least one .craftgo file in %s declaring `package X` (types, services)\n", target)
	fmt.Printf("  3. run `craftgo gen -f %s` to generate types, handlers, routes, openapi\n", target)
	return nil
}

// initManifest renders the starter craftgo.design.yaml. The body has
// no template variables - every value is either a default that 90%
// of projects keep or a commented hint at an optional knob. The
// Go module path is read from go.mod at gen time, so the manifest
// itself carries no `package:` field.
//
// The body lives in `templates/craftgo.design.yaml.tmpl`; edit that
// file rather than hand-rolling the YAML in Go source.
func initManifest() string {
	return renderInitTemplate("craftgo.design.yaml.tmpl", nil)
}

// securitySchemeNames returns the keys of cfg.OpenAPI.SecuritySchemes
// in deterministic order. Returns nil when no schemes are declared so
// the semantic analyser can distinguish "no truth source" from "empty
// allow-list" (the former skips the check; the latter would reject
// every reference).
func securitySchemeNames(cfg *config.Config) []string {
	if cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.OpenAPI.SecuritySchemes))
	for name := range cfg.OpenAPI.SecuritySchemes {
		out = append(out, name)
	}
	return out
}

// parseDesign walks designDir for `.craftgo` files, parses each one, and
// returns the collected AST. Parser diagnostics are aggregated and returned
// as a single error so the caller doesn't see a half-parsed package.
func parseDesign(designDir string) ([]*ast.File, error) {
	var files []*ast.File
	var parseDiags []string
	walkErr := filepath.Walk(designDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".craftgo" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p := parser.New(path, string(data))
		f := p.Parse()
		for _, e := range p.Diagnostics() {
			parseDiags = append(parseDiags, fmt.Sprintf("  %s: %s", e.Pos.String(), e.Msg))
		}
		files = append(files, f)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(parseDiags) > 0 {
		return nil, fmt.Errorf("parse errors:\n%s", strings.Join(parseDiags, "\n"))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .craftgo files found under %s", designDir)
	}
	return files, nil
}

// formatSemanticErrors filters severity-error diagnostics out of
// `diags` and renders them as a single multi-line message suitable
// for `fmt.Errorf`. Returns "" when nothing surfaces - warnings,
// info, hints stay silent at this layer because the LSP shows them
// in the editor and forcing them onto stderr noise out CI logs.
func formatSemanticErrors(diags []semantic.Diagnostic) string {
	lines := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Severity == lexer.SeverityWarning || d.Severity == lexer.SeverityInfo || d.Severity == lexer.SeverityHint {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", d.Pos.String(), d.Msg))
	}
	if len(lines) == 0 {
		return ""
	}
	return "semantic errors:\n" + strings.Join(lines, "\n")
}
