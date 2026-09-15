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

// runFmt is the `craftgo fmt [path] [-l] [-w]` entry point. Behaviour mirrors
// `gofmt`:
//
//   - Default mode is `-w`: every .craftgo file under <path> is formatted
//     in place.
//   - With `-l`, files that WOULD change are listed and the binary exits
//     non-zero if any are listed (suitable for CI).
//   - Combining `-l -w` lists changed files AND writes them.
//   - Without either flag, formatted output is printed to stdout.
//
// Path defaults to "." and is recursed when it is a directory; when it
// points at a single file, only that file is processed.
func runFmt(args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	list := fs.Bool("l", false, "list files whose formatting differs from craftgo fmt")
	write := fs.Bool("w", false, "write result to source file (default true when no other flags set)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := "."
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	// Default behaviour when no flags supplied: write back.
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
	blocked := blockingDiagnostics(files)
	var changed []string
	skipped := 0
	for _, f := range files {
		if msgs := blocked[f]; len(msgs) > 0 {
			skipped++
			fmt.Fprintf(os.Stderr, "%s: not formatted, fix these first:\n", f)
			for _, m := range msgs {
				fmt.Fprintf(os.Stderr, "  %s\n", m)
			}
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		formatted, _ := format.Format(f, string(raw))
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
		} else if !*list {
			fmt.Print(formatted)
		}
	}
	if skipped > 0 {
		return fmt.Errorf("%d file(s) left unformatted because of errors", skipped)
	}
	if *list && len(changed) > 0 {
		// Mirror `gofmt -l`: non-zero exit when any file is mis-formatted,
		// so CI can `craftgo fmt -l` as a check.
		os.Exit(1)
	}
	return nil
}

// blockingDiagnostics returns, per file, the errors that keep it from
// being formatted: parser and analyser errors alike, because a mistake
// the parser tolerates (a stray word read as a mixin, a decorator on the
// wrong line) reads as a different construct, and formatting would write
// that reading back. A file inside a project is analysed with its whole
// project, so cross-package references resolve; a file outside any
// project is analysed on its own.
func blockingDiagnostics(files []string) map[string][]string {
	out := map[string][]string{}
	add := func(diags []lexer.Diagnostic, fallback string) {
		for _, d := range diags {
			if !d.IsError() {
				continue
			}
			key := d.Pos.Filename
			if key == "" {
				key = fallback
			}
			out[key] = append(out[key], fmt.Sprintf("%s: %s", d.Pos, d.Msg))
		}
	}
	analysed := map[string]bool{}
	for _, f := range files {
		cfg, _, designDir, err := config.Find(filepath.Dir(f))
		if err != nil {
			data, readErr := os.ReadFile(f)
			if readErr != nil {
				continue
			}
			p := parser.New(f, string(data))
			file := p.Parse()
			add(p.Diagnostics(), f)
			_, diags := semantic.Analyze([]*ast.File{file})
			add(diags, f)
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
		add(diags, f)
	}
	return out
}

// collectCraftgoFiles returns every `*.craftgo` file under target. If target
// is itself a file, the slice contains just that file (regardless of
// extension - callers already opted into formatting it).
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
