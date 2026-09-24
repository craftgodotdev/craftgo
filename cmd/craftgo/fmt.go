package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/format"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// runFmt formats in place the design files under a path (default ".") or the
// file it names, failing on files with errors, which stay untouched. `-l` lists
// the files that differ instead and returns errFilesDiffer if any do; `-l -w`
// does both.
func runFmt(args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	list := fs.Bool("l", false, "list files whose formatting differs from craftgo fmt")
	write := fs.Bool("w", false, "write result to source file (default true when no other flags set)")
	path, err := parseArgs(fs, args, ".")
	if err != nil {
		return err
	}
	if !*list && !*write {
		*write = true
	}
	files, err := collectCraftgoFiles(path)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .craftgo files found under %q", path)
	}
	blocked, err := blockingDiagnostics(files)
	if err != nil {
		return err
	}
	var changed []string
	skipped := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		formatted, diags := format.Format(f, string(raw))
		if blockers := blocked[f]; len(blockers) > 0 {
			diags = blockers
		}
		if len(diags) > 0 {
			skipped++
			fmt.Fprintf(os.Stderr, "%s: not formatted, fix these first:\n", f)
			for _, d := range diags {
				fmt.Fprintf(os.Stderr, "  %s\n", d.Error())
			}
			continue
		}
		if formatted == string(raw) {
			continue
		}
		changed = append(changed, f)
		if *list {
			fmt.Println(f)
		}
		if *write {
			if err := os.WriteFile(f, []byte(formatted), 0o644); err != nil {
				return err
			}
		}
	}
	if skipped > 0 {
		return fmt.Errorf("%d file(s) left unformatted because of errors", skipped)
	}
	if *list && len(changed) > 0 {
		return errFilesDiffer
	}
	return nil
}

// blockingDiagnostics returns, per file as given, the parser and analyser
// errors that keep it from being formatted. A file in a project is analysed
// with the whole project, any other file on its own.
func blockingDiagnostics(files []string) (map[string][]lexer.Diagnostic, error) {
	given := make(map[string]string, len(files))
	absPaths := make([]string, len(files))
	for i, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		given[abs] = f
		absPaths[i] = abs
	}
	out := map[string][]lexer.Diagnostic{}
	add := func(diags []lexer.Diagnostic, fallback string) {
		for _, d := range diags {
			key := d.Pos.Filename
			if key == "" {
				key = fallback
			}
			if f, ok := given[key]; ok && d.IsError() {
				out[f] = append(out[f], d)
			}
		}
	}
	analysed := map[string]bool{}
	for _, abs := range absPaths {
		cfg, _, designDir, err := config.Find(filepath.Dir(abs))
		if err != nil {
			data, readErr := os.ReadFile(abs)
			if readErr != nil {
				continue
			}
			p := parser.New(abs, string(data))
			file := p.Parse()
			add(p.Diagnostics(), abs)
			_, diags := semantic.Analyze([]*ast.File{file})
			add(diags, abs)
			continue
		}
		if analysed[designDir] {
			continue
		}
		analysed[designDir] = true
		srcs, err := designopts.Load(designDir)
		if err != nil {
			continue
		}
		_, _, diags := designopts.Analyze(srcs, designDir, cfg)
		add(diags, abs)
	}
	return out, nil
}

// collectCraftgoFiles returns every design file under target, or target itself
// when it is a file, whatever its extension.
func collectCraftgoFiles(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	var out []string
	err = filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if config.IsDesignFile(p) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return out, nil
}
