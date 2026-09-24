// Package designopts finds, loads, parses and analyses the design files under
// a design root, and turns a manifest into the analyser's options.
package designopts

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// For returns the analyser options cfg sets for the design at designRoot. A nil
// cfg, or one declaring no security schemes, leaves SecuritySchemes nil, which
// turns the `@security` reference check off.
func For(designRoot string, cfg *config.Config) semantic.Options {
	return semantic.Options{
		SecuritySchemes: securitySchemeNames(cfg),
		BasePath:        basePath(cfg),
		DesignRoot:      designRoot,
		FileCase:        fileCase(cfg),
	}
}

// securitySchemeNames returns the manifest's scheme names sorted, or nil when
// it declares none.
func securitySchemeNames(cfg *config.Config) []string {
	if cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(cfg.OpenAPI.SecuritySchemes))
}

func basePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.OpenAPI.BasePath
}

func fileCase(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Output.FileCase
}

// Source is one design file and the text to analyse: the disk copy, or an
// editor's buffer when one is open.
type Source struct {
	Path string
	Text string
}

// Files lists every design file under designRoot in walk order. A directory it
// cannot read fails the call with nil paths; [FilesBestEffort] skips it instead.
func Files(designRoot string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(designRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && config.IsDesignFile(path) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FilesBestEffort lists every design file under designRoot it can read, in
// walk order, skipping unreadable directories.
func FilesBestEffort(designRoot string) []string {
	var out []string
	_ = filepath.WalkDir(designRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && config.IsDesignFile(path) {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// Load reads every design file under designRoot from disk.
func Load(designRoot string) ([]Source, error) {
	paths, err := Files(designRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, Source{Path: p, Text: string(data)})
	}
	return out, nil
}

// Parsed is one parsed design file: its AST and its tokens.
type Parsed struct {
	File   *ast.File
	Tokens []lexer.Token
}

// Parse parses srcs in order and returns them with every parser diagnostic. A
// file without a `package` clause keeps none; [semantic.AnalyzeProject] places it.
func Parse(srcs []Source) ([]Parsed, []lexer.Diagnostic) {
	out := make([]Parsed, 0, len(srcs))
	var diags []lexer.Diagnostic
	for _, s := range srcs {
		p := parser.New(s.Path, s.Text)
		out = append(out, Parsed{File: p.Parse(), Tokens: p.Tokens()})
		diags = append(diags, p.Diagnostics()...)
	}
	return out, diags
}

// ASTs returns the ASTs of parsed.
func ASTs(parsed []Parsed) []*ast.File {
	out := make([]*ast.File, 0, len(parsed))
	for _, p := range parsed {
		out = append(out, p.File)
	}
	return out
}

// Analyze parses srcs and analyses them as one project rooted at designRoot,
// returning the parser diagnostics followed by the semantic ones.
func Analyze(srcs []Source, designRoot string, cfg *config.Config) (*semantic.Project, []Parsed, []lexer.Diagnostic) {
	parsed, diags := Parse(srcs)
	proj, semDiags := semantic.AnalyzeProject(ASTs(parsed), For(designRoot, cfg))
	return proj, parsed, append(diags, semDiags...)
}
