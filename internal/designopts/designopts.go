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
	"io/fs"
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

// Files lists every design file under designRoot, in walk order. A
// directory it cannot read fails the call and the paths are nil: a tool
// that GENERATES from a design must not generate from half of one, and a
// partial list is the input that would let it.
//
// [FilesBestEffort] is the other policy, for a caller that has to keep
// working on a tree it can only partly see. The difference between them
// is one line, and it is the whole decision.
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

// FilesBestEffort lists every design file it can read, in walk order,
// SKIPPING a directory it cannot rather than stopping at it - so a file
// after the unreadable one is still found.
//
// It returns no error, deliberately. A caller on this policy has already
// decided it will carry on, and an error it must then discard is
// indistinguishable from one it dropped by accident - which is exactly
// how the editor came to analyse a truncated project and invent
// unknown-symbol errors for types it simply had not read.
func FilesBestEffort(designRoot string) []string {
	var out []string
	_ = filepath.WalkDir(designRoot, func(path string, d fs.DirEntry, err error) error {
		// Returning nil for the error the walk reports is what continues
		// past an unreadable directory; returning the error stops there.
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
