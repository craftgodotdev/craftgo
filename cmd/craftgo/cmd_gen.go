package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/codegen"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/protodesign"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// targetList collects a repeatable `--target` flag.
type targetList []string

func (t *targetList) String() string { return strings.Join(*t, ",") }

func (t *targetList) Set(v string) error {
	for _, name := range strings.Split(v, ",") {
		if name = strings.TrimSpace(name); name != "" {
			*t = append(*t, name)
		}
	}
	return nil
}

func parseGenArgs(args []string) (manifest, ctxRoot, positional string, targets targetList, err error) {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	fs.Var(&targets, "target", "generate only the named target ("+strings.Join(codegen.SelectableTargets(), ", ")+"); repeatable, default all")
	fs.StringVar(&manifest, "f", "", "design folder holding craftgo.design.yaml (skips walk-up)")
	fs.StringVar(&manifest, "folder", "", "alias for -f")
	fs.StringVar(&ctxRoot, "c", "", "project root the output paths resolve against (defaults to the parent of the design folder)")
	fs.StringVar(&ctxRoot, "context", "", "alias for -c")
	if perr := fs.Parse(args); perr != nil {
		return "", "", "", nil, parseFlagError("gen", perr)
	}
	rest := fs.Args()
	switch len(rest) {
	case 0:
		positional = "."
	case 1:
		positional = rest[0]
	default:
		return "", "", "", nil, fmt.Errorf("gen: too many positional arguments (got %d, want at most 1)", len(rest))
	}
	return manifest, ctxRoot, positional, targets, nil
}

func findManifest(manifestFolder, contextRoot, target string) (*config.Config, string, string, error) {
	if manifestFolder != "" {
		// An empty contextRoot resolves to the design folder's parent, never the
		// working directory.
		return config.FindAt(manifestFolder, contextRoot)
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

// runGen loads the manifest, analyses the design's `.craftgo` and `.proto`
// files and runs [codegen.Generate].
func runGen(args []string) error {
	manifestFolder, contextRoot, target, targets, err := parseGenArgs(args)
	if err != nil {
		return err
	}
	cfg, projectRoot, designDir, err := findManifest(manifestFolder, contextRoot, target)
	if err != nil {
		return err
	}
	modulePath, err := config.ResolveModulePath(projectRoot)
	if err != nil {
		return err
	}
	cfg.Package = modulePath

	protos, err := protodesign.Load(context.Background(), designDir, designopts.ProtoOptions(cfg, projectRoot))
	if err != nil {
		return err
	}
	// A design of protos alone has an empty DSL project.
	proj, err := analyzeDesign(designDir, cfg, protos != nil)
	if err != nil {
		return err
	}
	in := codegen.Inputs{Design: proj, Protos: protos}
	if err := codegen.Generate(in, cfg, projectRoot, targets...); err != nil {
		return err
	}
	fmt.Printf("craftgo: generated %d package(s)%s under %s\n", len(proj.Packages), grpcSummary(protos), projectRoot)
	for _, note := range codegen.OutputNotes(in, cfg, projectRoot) {
		fmt.Println("craftgo: " + note)
	}
	return nil
}

// grpcSummary is the gRPC half of the run summary, empty without protos.
func grpcSummary(protos *protodesign.Set) string {
	if protos == nil {
		return ""
	}
	return fmt.Sprintf(", %d gRPC service(s)", len(protos.Services))
}

// analyzeDesign parses and analyses the design files under designDir, folding
// the errors into one. A design with no DSL package is an error unless
// allowEmpty is set.
func analyzeDesign(designDir string, cfg *config.Config, allowEmpty bool) (*semantic.Project, error) {
	files, err := parseDesign(designDir, allowEmpty)
	if err != nil {
		return nil, err
	}
	// A file-level `@version` or `@doc` overrides openapi.version or
	// openapi.description.
	if cfg != nil {
		if v := fileDecoratorString(files, "version"); v != "" {
			cfg.OpenAPI.Version = v
		}
		if d := fileDecoratorString(files, "doc"); d != "" {
			cfg.OpenAPI.Description = d
		}
	}
	proj, diags := semantic.AnalyzeProject(files, designopts.For(designDir, cfg))
	if errs := formatSemanticErrors(diags); errs != "" {
		return nil, fmt.Errorf("%s", errs)
	}
	if len(proj.Packages) == 0 && !allowEmpty {
		return nil, fmt.Errorf("project has no DSL packages - every project must have at least one .craftgo file declaring `package X`, or a .proto declaring a service")
	}
	return proj, nil
}

// fileDecoratorString returns the first non-empty string argument of a
// file-level `@<name>` across files, or "".
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

// parseDesign parses the design files under designDir, folding every parser
// diagnostic into one error. With allowEmpty, finding no file is not an error.
func parseDesign(designDir string, allowEmpty bool) ([]*ast.File, error) {
	srcs, err := designopts.Load(designDir)
	if err != nil {
		return nil, err
	}
	parsed, diags := designopts.Parse(srcs)
	files := designopts.ASTs(parsed)
	var parseDiags []string
	for _, e := range diags {
		parseDiags = append(parseDiags, fmt.Sprintf("  %s: %s", e.Pos.String(), e.Msg))
	}
	if len(parseDiags) > 0 {
		return nil, fmt.Errorf("parse errors:\n%s", strings.Join(parseDiags, "\n"))
	}
	if len(files) == 0 {
		if allowEmpty {
			return nil, nil
		}
		return nil, fmt.Errorf("no .craftgo or .proto files found under %s", designDir)
	}
	return files, nil
}

// formatSemanticErrors renders the error-severity diagnostics as one message,
// or "" when there are none.
func formatSemanticErrors(diags []semantic.Diagnostic) string {
	lines := make([]string, 0, len(diags))
	for _, d := range diags {
		if !d.IsError() {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", d.Pos.String(), d.Msg))
	}
	if len(lines) == 0 {
		return ""
	}
	return "semantic errors:\n" + strings.Join(lines, "\n")
}
