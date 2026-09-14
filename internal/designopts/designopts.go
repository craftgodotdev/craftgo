// Package designopts is where a design is discovered, loaded, parsed and
// analysed - once, for every caller. `craftgo gen`, `craftgo fmt` and the
// language server all reach a [semantic.Project] through here, so the
// editor diagnoses the design the CLI generates from.
//
// It exists as its own package rather than inside `semantic` because the
// analyser is deliberately manifest-blind: [semantic.Options] documents
// "pass an empty Options for the default", and its only edges to `config`
// are two leaf helpers that never touch the Config struct. Teaching it to
// read manifest schema would change that edge in kind. `config` cannot
// host it either - `semantic` imports `config`, so the reverse is a
// cycle.
package designopts

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// For returns the analyser options cfg configures for the design rooted
// at designRoot.
//
// designRoot is passed separately rather than read off the manifest: a
// projection generates from the design folder its manifest NAMES, not the
// one it sits in, so the two are different paths.
//
// A nil cfg is a design with no manifest - a single file open in an
// editor, or a folder below no project. The options then carry a nil
// SecuritySchemes, which is what tells the reference check to stay quiet
// rather than reporting every scheme as undeclared.
func For(designRoot string, cfg *config.Config) semantic.Options {
	return semantic.Options{
		SecuritySchemes: securitySchemeNames(cfg),
		BasePath:        basePath(cfg),
		DesignRoot:      designRoot,
		FileCase:        fileCase(cfg),
	}
}

// securitySchemeNames lists the manifest's declared scheme names, sorted.
// The order is user-visible: the reference check renders the list into
// its "known: ..." text, and a map's order would shuffle the message
// between runs.
func securitySchemeNames(cfg *config.Config) []string {
	if cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.OpenAPI.SecuritySchemes))
	for name := range cfg.OpenAPI.SecuritySchemes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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

// Files lists every design file under designRoot, in walk order. Whatever
// was found before an unreadable directory is returned alongside the
// error, so a caller that would rather carry on can.
func Files(designRoot string) ([]string, error) {
	var out []string
	err := filepath.Walk(designRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && config.IsDesignFile(path) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
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

// Parsed is one parsed design file: its AST and the token stream behind
// it. The tokens are what the editor's position-sensitive features read;
// they ride along so nothing has to parse a second time to get them.
type Parsed struct {
	File   *ast.File
	Tokens []lexer.Token
}

// Parse parses srcs in order and returns them with every parser
// diagnostic.
//
// A file that declares no `package` is left as it is. Which package it
// belongs to is [semantic.AnalyzeProject]'s rule - it joins the project's
// only named package when there is exactly one - and naming it here would
// pre-empt that rule rather than support it.
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

// ASTs is the files of parsed, for a caller with no use for the tokens.
func ASTs(parsed []Parsed) []*ast.File {
	out := make([]*ast.File, 0, len(parsed))
	for _, p := range parsed {
		out = append(out, p.File)
	}
	return out
}

// Analyze parses srcs and analyses them as the one project rooted at
// designRoot, returning the parser and semantic diagnostics together in
// that order.
func Analyze(srcs []Source, designRoot string, cfg *config.Config) (*semantic.Project, []Parsed, []lexer.Diagnostic) {
	parsed, diags := Parse(srcs)
	proj, semDiags := semantic.AnalyzeProject(ASTs(parsed), For(designRoot, cfg))
	return proj, parsed, append(diags, semDiags...)
}
