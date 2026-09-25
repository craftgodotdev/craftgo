package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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

// genArgs are the arguments of `craftgo gen`.
type genArgs struct {
	folder  string // -f: the design folder; no walk-up
	context string // -c: the project root in place of the design folder's parent
	path    string // where the walk-up starts
	targets targetList
}

func parseGenArgs(args []string) (genArgs, error) {
	var a genArgs
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	fs.Var(&a.targets, "target", "generate only the named target ("+strings.Join(codegen.SelectableTargets(), ", ")+"); repeatable, default all")
	fs.StringVar(&a.folder, "f", "", "design folder holding craftgo.design.yaml (skips walk-up)")
	fs.StringVar(&a.folder, "folder", "", "alias for -f")
	fs.StringVar(&a.context, "c", "", "project root the output paths resolve against (defaults to the parent of the design folder)")
	fs.StringVar(&a.context, "context", "", "alias for -c")
	path, err := parseArgs(fs, args, ".")
	if err != nil {
		return genArgs{}, err
	}
	if a.folder != "" && fs.NArg() > 0 {
		return genArgs{}, badArgs(fs, "path %q given with -f, which names the design folder", path)
	}
	a.path = path
	return a, nil
}

// findManifest loads the manifest a names and returns it with the absolute
// project root and design folder.
func findManifest(a genArgs) (*config.Config, string, string, error) {
	var (
		cfg                    *config.Config
		projectRoot, designDir string
		err                    error
	)
	if a.folder != "" {
		cfg, projectRoot, designDir, err = config.FindAt(a.folder)
	} else {
		cfg, projectRoot, designDir, err = config.Find(a.path)
	}
	if err != nil {
		return nil, "", "", err
	}
	if a.context != "" {
		if projectRoot, err = filepath.Abs(a.context); err != nil {
			return nil, "", "", err
		}
	}
	return cfg, projectRoot, designDir, nil
}

// runGen loads the manifest, analyses the design's `.craftgo` and `.proto`
// files and runs [codegen.Generate].
func runGen(args []string) error {
	a, err := parseGenArgs(args)
	if err != nil {
		return err
	}
	cfg, projectRoot, designDir, err := findManifest(a)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(os.Stderr, "craftgo: warning: "+w)
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
	if err := codegen.Generate(in, cfg, projectRoot, a.targets...); err != nil {
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
		if s, ok := ast.StringArg(f.Decorators, name); ok && s != "" {
			return s
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
