package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/format"
	"github.com/craftgodotdev/craftgo/internal/lexer"
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
	projects := map[string][]lexer.Diagnostic{}
	var changed []string
	skipped := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		formatted, diags := format.Format(f, string(raw))
		if len(diags) == 0 {
			if diags, err = analysisErrors(f, string(raw), projects); err != nil {
				return err
			}
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

// analysisErrors returns the [designopts.FileErrors] of file, holding src, in
// the analysis of its project, or of the file alone outside any project.
// projects caches each project's diagnostics by design root.
func analysisErrors(file, src string, projects map[string][]lexer.Diagnostic) ([]lexer.Diagnostic, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	cfg, root := designopts.ProjectOf(abs)
	if root == "" {
		_, _, diags := designopts.Analyze([]designopts.Source{{Path: abs, Text: src}}, "", nil)
		return designopts.FileErrors(diags, abs), nil
	}
	diags, ok := projects[root]
	if !ok {
		srcs, err := designopts.Load(root)
		if err != nil {
			return nil, err
		}
		_, _, diags = designopts.Analyze(srcs, root, cfg)
		projects[root] = diags
	}
	return designopts.FileErrors(diags, abs), nil
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
