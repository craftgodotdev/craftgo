// craftgo gen subcommand: design parse, semantic analysis, per-package + project-wide codegen.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/codegen"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

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

// runGen resolves the manifest, analyses the design, and hands the
// validated project to [codegen.Generate].
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
	if err := codegen.Generate(proj, cfg, projectRoot); err != nil {
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
	// A file-header `@version("X")` overrides craftgo.design.yaml's
	// openapi.version (the decorator's documented contract). Applied to
	// cfg before codegen so the OpenAPI info.version honours it instead of
	// silently dropping the decorator.
	if cfg != nil {
		if v := fileDecoratorString(files, "version"); v != "" {
			cfg.OpenAPI.Version = v
		}
		if d := fileDecoratorString(files, "doc"); d != "" {
			cfg.OpenAPI.Description = d
		}
	}
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{
		SecuritySchemes: securitySchemeNames(cfg),
		BasePath:        cfg.OpenAPI.BasePath,
		DesignRoot:      designDir,
		FileCase:        cfg.Output.FileCase,
	})
	if errs := formatSemanticErrors(diags); errs != "" {
		return nil, fmt.Errorf("%s", errs)
	}
	if len(proj.Packages) == 0 {
		return nil, fmt.Errorf("project has no DSL packages - every project must have at least one .craftgo file declaring `package X`")
	}
	return proj, nil
}

// fileDecoratorString returns the string argument of the first file-header
// `@<name>("X")` decorator across the design files, or "" when none is
// present. Used for the file-level OpenAPI overrides - `@version` (document
// version) and `@doc` (info.description) - that override the
// craftgo.design.yaml values.
func fileDecoratorString(files []*ast.File, name string) string {
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, d := range f.Decorators {
			if d == nil || d.Name != name || len(d.Args) == 0 {
				continue
			}
			if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
				return s.Value
			}
		}
	}
	return ""
}

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
		if !config.IsDesignFile(path) {
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
