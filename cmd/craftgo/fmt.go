package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/craftgodotdev/craftgo/internal/format"
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
	var changed []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		formatted, diags := format.Format(f, string(raw))
		if len(diags) > 0 {
			fmt.Fprintf(os.Stderr, "%s: skipped (parse errors)\n", f)
			for _, d := range diags {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", d.Pos, d.Msg)
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
		} else if !*list {
			fmt.Print(formatted)
		}
	}
	if *list && len(changed) > 0 {
		// Mirror `gofmt -l`: non-zero exit when any file is mis-formatted,
		// so CI can `craftgo fmt -l` as a check.
		os.Exit(1)
	}
	return nil
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
		if filepath.Ext(p) == ".craftgo" {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return out, nil
}
