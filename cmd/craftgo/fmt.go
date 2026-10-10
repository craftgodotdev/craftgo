package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/designopts"
	"github.com/craftgodotdev/craftgo/internal/format"
	"github.com/craftgodotdev/craftgo/internal/genfile"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/walk"
)

// runFmt formats in place the design files under a path (default ".") or the
// file it names, failing on files with errors, which stay untouched. `-l` lists
// the files that differ instead and returns errFilesDiffer if any do; `-l -w`
// does both, and an explicit `-w=false` prints the formatted text instead, as
// gofmt does. A file it cannot read or write is reported, and the run goes on
// and fails at the end.
func runFmt(args []string) error {
	flags := flag.NewFlagSet("fmt", flag.ContinueOnError)
	list := flags.Bool("l", false, "list files whose formatting differs from craftgo fmt")
	write := flags.Bool("w", false, "write result to source file (default true when no other flags set)")
	path, err := parseArgs(flags, args, ".")
	if err != nil {
		return err
	}
	writeSet := false
	flags.Visit(func(f *flag.Flag) { writeSet = writeSet || f.Name == "w" })
	show := writeSet && !*write && !*list
	if !writeSet {
		*write = !*list
	}
	files, err := collectCraftgoFiles(path)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		// A design of protos alone has nothing to format.
		if holdsDesign(path) {
			return nil
		}
		return fmt.Errorf("no .craftgo files found under %q", path)
	}
	projects := map[string]*project{}
	var changed []string
	skipped, failed := 0, 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			failed++
			fmt.Fprintln(os.Stderr, fileError(f, err))
			continue
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
		if show {
			fmt.Print(formatted)
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
			if err := genfile.Commit([]genfile.File{{Path: f, Body: []byte(formatted)}}); err != nil {
				failed++
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}
	var problems []string
	if skipped > 0 {
		problems = append(problems, fmt.Sprintf("%d file(s) left unformatted because of errors", skipped))
	}
	if failed > 0 {
		problems = append(problems, fmt.Sprintf("%d file(s) could not be read or written", failed))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	if *list && len(changed) > 0 {
		return errFilesDiffer
	}
	return nil
}

// fileError reports a file fmt could not read, naming it as the run does.
func fileError(path string, err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return fmt.Sprintf("%s: %v", path, err)
}

// project is a design project's sources and the diagnostics of their
// analysis.
type project struct {
	srcs  []designopts.Source
	diags []lexer.Diagnostic
}

// analysisErrors returns the [designopts.FileErrors] of file, holding src, in
// the analysis of the project that loads it, or of the file alone when none
// does, each placed in file as spelled. projects caches each project by design
// root.
func analysisErrors(file, src string, projects map[string]*project) ([]lexer.Diagnostic, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	if cfg, root := designopts.ProjectOf(abs); root != "" {
		p, ok := projects[root]
		if !ok {
			srcs, err := designopts.Load(root)
			if err != nil {
				return nil, err
			}
			_, _, diags := designopts.Analyze(srcs, root, cfg)
			p = &project{srcs: srcs, diags: diags}
			projects[root] = p
		}
		if path, ok := designopts.SourcePath(p.srcs, abs); ok {
			return spelledAs(designopts.FileErrors(p.diags, path), path, file), nil
		}
	}
	_, _, diags := designopts.Analyze([]designopts.Source{{Path: abs, Text: src}}, "", nil)
	return spelledAs(designopts.FileErrors(diags, abs), abs, file), nil
}

// spelledAs places each of diags in path at file, the spelling the run prints.
func spelledAs(diags []lexer.Diagnostic, path, file string) []lexer.Diagnostic {
	for i := range diags {
		if diags[i].Pos.Filename == path {
			diags[i].Pos.Filename = file
		}
	}
	return diags
}

// holdsDesign reports whether the directory path is a design folder, lies in
// one or holds one as a direct subdirectory.
func holdsDesign(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	_, _, design, err := config.Find(abs)
	if err != nil {
		return false
	}
	sep := string(filepath.Separator)
	return abs == design || strings.HasPrefix(abs, design+sep) || strings.HasPrefix(design, abs+sep)
}

// collectCraftgoFiles returns every design file a [walk.Design] walk of target
// finds, warning on stderr of a directory it cannot read, or target itself
// when it is a file, whatever its extension - unless it is plainly not a
// design file.
func collectCraftgoFiles(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if foreignFile(target) {
			return nil, fmt.Errorf("%s is not a design file - fmt formats .craftgo and .cg files", target)
		}
		return []string{target}, nil
	}
	var out []string
	err = walk.Files(target, walk.Design(func(path string, err error) error {
		fmt.Fprintln(os.Stderr, "craftgo: warning: skipping "+fileError(path, err))
		return nil
	}), func(path string) error {
		if config.IsDesignFile(path) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// foreignFile reports a file fmt refuses even when named on its own: Go
// source, a proto, YAML such as the manifest, and the go command's own files.
func foreignFile(path string) bool {
	switch filepath.Base(path) {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".proto", ".yaml", ".yml":
		return true
	}
	return false
}
