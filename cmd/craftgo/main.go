// Command craftgo is the CLI entrypoint that drives the design-first
// pipeline: locate the project manifest, parse every `.craftgo` source
// file, run semantic analysis, and dispatch each codegen artefact.
//
// Usage:
//
//	craftgo init [path] [-package <module>]
//	craftgo gen  [path]
//
// `init` scaffolds a fresh design folder under <path> (default cwd) with a
// craftgo.design.yaml, sample type, and sample service. Existing files are
// never overwritten — re-running on a populated directory is a no-op for
// any pre-existing artefact.
//
// `gen` searches the current working directory upward (or, when given,
// <path>) for a craftgo.design.yaml (or a sibling `design/` folder
// containing one) and runs the full codegen pipeline.
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
  craftgo init [path] [-package <module>]
                          Scaffold design/ with a sample manifest, type, service
  craftgo gen [path]      Generate types, handlers, routes, OpenAPI from .craftgo
  craftgo fmt [path] [-l] [-w]
                          Canonical-format .craftgo files (default: write back)
  craftgo version         Print the CLI version
  craftgo help            Show this message

For 'gen', path is optional; when omitted craftgo searches the current
directory upward for a craftgo.design.yaml (or a sibling design/ folder
containing one). For 'init', path defaults to '.' and -package defaults to
'github.com/example/app' (which you should edit immediately). For 'fmt',
path may be a single file or a directory (recursed for *.craftgo).`)
}

// runGen wires the full design → codegen pipeline. The implementation is a
// straight-line list rather than a generic dispatcher because each phase
// has subtly different argument shapes (some need a project root, some take
// only an outDir) and the order matters: types first so handlers and routes
// can reference them, then OpenAPI last so it has the final symbol table.
func runGen(args []string) error {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	cfg, projectRoot, designDir, err := config.Find(target)
	if err != nil {
		return err
	}
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
			{"errors(" + name + ")", func() error { return codegen.GenerateErrors(p, typesDir) }},
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

	// Project-wide artefacts: routes-umbrella, main.go, openapi.yaml.
	// These aggregate services + types across every package so they
	// must run AFTER all per-package gen has produced the upstream
	// inputs.
	if err := codegen.GenerateProjectRoutesUmbrella(proj, cfg, projectRoot); err != nil {
		return fmt.Errorf("routes-umbrella: %w", err)
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

// runInit scaffolds a fresh design directory under args[0] (default ".").
// `-package <module>` overrides the placeholder Go-module path. The
// command refuses to overwrite any pre-existing file: each artefact is
// written only when its destination doesn't already exist, so re-running
// on a partially populated tree fills only the gaps. The intent is that
// new users can scaffold once, edit the placeholder package path, and
// then immediately run `craftgo gen`.
func runInit(args []string) error {
	pkgPath := "github.com/example/app"
	target := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-package", "--package":
			if i+1 >= len(args) {
				return fmt.Errorf("init: -package requires a value")
			}
			pkgPath = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("init: unknown flag %q", args[i])
			}
			target = args[i]
		}
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	designDir := filepath.Join(abs, "design")
	if err := os.MkdirAll(filepath.Join(designDir, "types"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(designDir, "services"), 0o755); err != nil {
		return err
	}

	// (relative-path, content) tuples. Each entry is written only when the
	// file doesn't already exist so re-runs don't clobber edits.
	files := []struct {
		path    string
		content string
	}{
		{
			"design/craftgo.design.yaml",
			initManifest(pkgPath),
		},
		{"design/api.craftgo", initAPICraftgo()},
		{"design/user.craftgo", initTypesCraftgo()},
		{"design/user-service.craftgo", initServiceCraftgo()},
	}

	written, skipped := 0, 0
	for _, f := range files {
		dest := filepath.Join(abs, f.path)
		if _, err := os.Stat(dest); err == nil {
			skipped++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(f.content), 0o644); err != nil {
			return err
		}
		written++
	}
	fmt.Printf("craftgo: init wrote %d file(s), skipped %d existing under %s\n", written, skipped, abs)
	if written > 0 {
		fmt.Println("next steps:")
		fmt.Println("  1. edit design/craftgo.design.yaml (especially `package:`)")
		fmt.Println("  2. run `craftgo gen` to generate types, handlers, routes, openapi")
	}
	return nil
}

// initManifest renders the starter craftgo.design.yaml. The package path
// is the only value that varies per project; everything else is the
// framework default and can stay as-is for 90% of projects.
func initManifest(pkgPath string) string {
	return `package: ` + pkgPath + `
output:
  types:      ./internal/types
  handler:    ./internal/handler
  routes:     ./internal/routes
  logic:      ./internal/logic
  middleware: ./internal/middleware
  svccontext: ./svccontext/svccontext.go
  openapi:    ./docs/openapi.yaml
openapi:
  title:    My API
  version:  1.0.0
  basePath: /api
`
}

// initAPICraftgo is the file-scope DSL: file-level decorators and the
// package declaration. Subfolders contribute their own files; the
// import directives below merge them into one logical package.
func initAPICraftgo() string {
	return `@title("My API")
@version("1.0.0")
@doc("Generated by craftgo init.")
package design
`
}

// initTypesCraftgo is a tiny but realistic starter type — enough to show
// validation decorators, optional fields, and JSON-tag conventions.
func initTypesCraftgo() string {
	return `package design

// User is the canonical example domain entity.
type User {
    id        string @required
    email     string @required @format(email)
    name      string @required @length(1, 80)
    createdAt string @format(datetime)
}
`
}

// initServiceCraftgo wires one verb of each shape (GET + POST) so a fresh
// project's first `craftgo gen` produces working JSON handlers out of
// the box.
func initServiceCraftgo() string {
	return `package design

@prefix("/v1")
@tags(users)
service UserService {
    // GetUser returns the user identified by {id}.
    @doc("Look up a user by id.")
    get GetUser /users/{id} {
        response  User
    }

    // CreateUser stores a new user record.
    @doc("Create a new user.")
    post CreateUser /users {
        request   User
        response  User
    }
}
`
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
