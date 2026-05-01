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
// The path argument IS the design folder — manifest + sample `.craftgo`
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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/codegen"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/lexer"
	"github.com/dropship-dev/craftgo/internal/parser"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// version is the CLI's reported version. Kept as a build-time constant for
// now; release tooling can override via `-ldflags="-X main.version=..."`.
const version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "gen":
		if err := runGen(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "craftgo: "+err.Error())
			os.Exit(1)
		}
	case "init":
		if err := runInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "craftgo: "+err.Error())
			os.Exit(1)
		}
	case "fmt":
		if err := runFmt(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "craftgo: "+err.Error())
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "craftgo: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// usage prints a short command summary to stdout. Verbose enough to remind
// returning users of the positional-path convention but not so detailed that
// it becomes a maintenance burden — full docs live in the README.
func usage() {
	fmt.Println(`craftgo — design-first Go API framework

Usage:
  craftgo init [path]
                          Scaffold a design folder at <path> (default: 'design').
                          The supplied path IS the design folder — the manifest
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
                          from go.mod, walking up from the project root —
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
//   - `-f <folder>`: explicit design folder (where craftgo.design.yaml
//     lives). Skips the walk-up search.
//   - `-c|--context <root>`: project root the `output:` paths resolve
//     against. Defaults to cwd when -f is given, otherwise to the
//     parent of the manifest folder (legacy).
//   - positional path: walk-up start (legacy compat). Defaults to
//     `.` when neither -f nor a positional is given.
//
// Returns (manifestFolder, contextRoot, positionalTarget, error).
// Exactly one of manifestFolder / positionalTarget is meaningful at
// the call site — the runGen dispatch picks the path based on which
// is set.
func parseGenArgs(args []string) (manifest, ctxRoot, positional string, err error) {
	positional = "."
	gotPositional := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--folder":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("gen: %s requires a folder argument", args[i])
			}
			manifest = args[i+1]
			i++
		case "-c", "--context":
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("gen: %s requires a path argument", args[i])
			}
			ctxRoot = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", fmt.Errorf("gen: unknown flag %q", args[i])
			}
			if gotPositional {
				return "", "", "", fmt.Errorf("gen: too many positional arguments")
			}
			positional = args[i]
			gotPositional = true
		}
	}
	return manifest, ctxRoot, positional, nil
}

// resolveGenPaths picks the design folder and project root from the
// parsed flags. Two flows:
//
//   - `-f <folder>` → design folder is exactly that path. Project
//     root defaults to cwd (the dir the user ran the command from)
//     so the monorepo layout — design at contracts/v1, code at repo
//     root — works without further flags. `-c` overrides.
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

// runGen wires the full design → codegen pipeline. The implementation is a
// straight-line list rather than a generic dispatcher because each phase
// has subtly different argument shapes (some need a project root, some take
// only an outDir) and the order matters: types first so handlers and routes
// can reference them, then OpenAPI last so it has the final symbol table.
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
	files, err := parseDesign(designDir)
	if err != nil {
		return err
	}
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{
		SecuritySchemes: securitySchemeNames(cfg),
		BasePath:        cfg.OpenAPI.BasePath,
		DesignRoot:      designDir,
	})
	if len(diags) > 0 {
		var sb strings.Builder
		sb.WriteString("semantic errors:\n")
		for _, d := range diags {
			if d.Severity == lexer.SeverityWarning || d.Severity == lexer.SeverityInfo || d.Severity == lexer.SeverityHint {
				continue
			}
			sb.WriteString("  ")
			sb.WriteString(d.Pos.String())
			sb.WriteString(": ")
			sb.WriteString(d.Msg)
			sb.WriteString("\n")
		}
		if sb.Len() > len("semantic errors:\n") {
			return fmt.Errorf("%s", sb.String())
		}
	}

	if len(proj.Packages) == 0 {
		return fmt.Errorf("project has no DSL packages — every project must have at least one .craftgo file declaring `package X`")
	}

	typesDir := filepath.Join(projectRoot, cfg.Output.Types)

	// Per-package types/enums/errors/validators. Each package gets
	// its own subdirectory under typesDir, with cross-package Go
	// imports inserted automatically when its DSL files reference
	// siblings.
	pkgNames := make([]string, 0, len(proj.Packages))
	for k := range proj.Packages {
		if k != "" {
			pkgNames = append(pkgNames, k)
		}
	}
	sort.Strings(pkgNames)

	// Security-scheme validation runs against every package whose
	// services declare `@security` — multi-package projects can spread
	// services across packages.
	for _, name := range pkgNames {
		p := proj.Packages[name]
		if p == nil {
			continue
		}
		if errs := codegen.ValidateSecurityRefs(p, cfg); len(errs) > 0 {
			var sb strings.Builder
			sb.WriteString("security scheme errors in package " + name + ":\n")
			for _, e := range errs {
				sb.WriteString("  ")
				sb.WriteString(e)
				sb.WriteString("\n")
			}
			return fmt.Errorf("%s", sb.String())
		}
	}

	for _, name := range pkgNames {
		p := proj.Packages[name]
		cross := codegen.BuildCrossPkg(proj, cfg, name)
		scalars := codegen.BuildScalarTable(proj, name)
		genSteps := []struct {
			name string
			fn   func() error
		}{
			{"types(" + name + ")", func() error { return codegen.GenerateTypesPackage(p, typesDir, cross) }},
			{"enums(" + name + ")", func() error { return codegen.GenerateEnums(p, typesDir) }},
			{"errors(" + name + ")", func() error { return codegen.GenerateErrorsPackage(p, typesDir, cross) }},
			{"validators(" + name + ")", func() error { return codegen.GenerateValidatorsWith(p, typesDir, cross, scalars) }},
		}
		for _, s := range genSteps {
			if err := s.fn(); err != nil {
				return fmt.Errorf("%s: %w", s.name, err)
			}
		}
	}

	// Service-shaped artefacts iterate every package that declares
	// services. Multiple packages may contribute services; codegen
	// handlers/routes/logic land in their own subdirectories. main.go
	// aggregates RegisterRoutes from all of them; openapi merges every
	// package's schema namespace with conflict-aware naming.
	// Middlewares are project-global (the semantic resolver enforces
	// uniqueness across packages). Generate the unified Middlewares
	// struct + scaffolds ONCE up-front so packages without services
	// — like `shared` — still contribute their declarations to
	// svccontext.
	if err := codegen.GenerateProjectMiddlewares(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("middlewares: %w", err)
	}
	for _, name := range pkgNames {
		p := proj.Packages[name]
		if len(p.Services) == 0 {
			continue
		}
		cross := codegen.BuildCrossPkg(proj, cfg, name)
		svcSteps := []struct {
			label string
			fn    func() error
		}{
			{"handlers(" + name + ")", func() error { return codegen.GenerateHandlersPackage(p, cfg, projectRoot, cross) }},
			{"handler-helpers(" + name + ")", func() error { return codegen.GenerateHandlerHelpers(p, cfg, projectRoot) }},
			{"logic(" + name + ")", func() error { return codegen.GenerateLogicPackage(p, cfg, projectRoot, cross) }},
			{"routes-svc(" + name + ")", func() error { return codegen.GeneratePerServiceRoutes(p, cfg, projectRoot) }},
		}
		for _, s := range svcSteps {
			if err := s.fn(); err != nil {
				return fmt.Errorf("%s: %w", s.label, err)
			}
		}
	}

	// Project-wide artefacts: routes-umbrella, runtime config + svccontext
	// scaffolds, main.go, openapi.yaml. These aggregate services + types
	// across every package so they must run AFTER all per-package gen
	// has produced the upstream inputs.
	//
	// Runtime scaffolds (config/, svccontext/) are gen-once and run
	// BEFORE main.go so the generated boot code can rely on the
	// config package's import path resolving. They self-skip when
	// `output.main: "-"` opts the project out of the runtime layer.
	if err := codegen.GenerateProjectRoutesUmbrella(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("routes-umbrella: %w", err)
	}
	if err := codegen.GenerateRuntimeConfig(cfg, projectRoot); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := codegen.GenerateSvccontext(cfg, projectRoot); err != nil {
		return fmt.Errorf("svccontext: %w", err)
	}
	if err := codegen.GenerateProjectMain(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("main: %w", err)
	}
	if err := codegen.GenerateProjectOpenAPI(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("openapi: %w", err)
	}
	fmt.Printf("craftgo: generated %d package(s) under %s\n", len(proj.Packages), projectRoot)
	return nil
}

// runInit scaffolds a fresh design folder at args[0]. The path argument
// IS the design folder — `craftgo init contracts/v1` creates
// `contracts/v1/craftgo.design.yaml` directly inside that directory;
// no intermediate `design/` wrapper. When no path is supplied the
// default is `design` (creates a `design/` subdir of cwd) so a fresh
// `mkdir myapp && cd myapp && craftgo init` produces the conventional
// layout.
//
// The command refuses to overwrite an existing manifest so re-running
// on a populated folder is a silent no-op. There is no `-package`
// flag — the Go module path is read from `go.mod` at gen time, so
// the only manifest-side configuration is the optional output paths
// and OpenAPI metadata.
//
// init only owns the manifest scaffolding — the runtime artefacts
// (config/, svccontext/, main.go) are scaffolded by `craftgo gen`
// using the same gen-once policy so they live in one place
// (internal/codegen/templates/) and follow the same workflow as
// every other generated artefact.
func runInit(args []string) error {
	target := "design"
	gotPositional := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("init: unknown flag %q", args[i])
			}
			if gotPositional {
				return fmt.Errorf("init: too many positional arguments")
			}
			target = args[i]
			gotPositional = true
		}
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
// no template variables — every value is either a default that 90%
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
	var diagBuf strings.Builder
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
		if d := p.Diagnostics(); len(d) > 0 {
			for _, e := range d {
				diagBuf.WriteString("  ")
				diagBuf.WriteString(e.Pos.String())
				diagBuf.WriteString(": ")
				diagBuf.WriteString(e.Msg)
				diagBuf.WriteString("\n")
			}
		}
		files = append(files, f)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if diagBuf.Len() > 0 {
		return nil, fmt.Errorf("parse errors:\n%s", diagBuf.String())
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .craftgo files found under %s", designDir)
	}
	return files, nil
}
