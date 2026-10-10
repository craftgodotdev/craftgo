// Package walk visits the regular files of a directory tree in lexical order,
// following symbolic links to directories: a design folder may link in a
// package folder another deployable shares, and an output directory may be a
// link to another part of the project.
package walk

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Options tunes [Files].
type Options struct {
	// SkipDir reports, by name, a directory not to enter; the root is always
	// entered.
	SkipDir func(name string) bool
	// SkipFile reports, by name, a file not to visit.
	SkipFile func(name string) bool
	// Within, when set, bounds the walk: a directory whose real path, links
	// resolved, lies outside it is not entered, the root included.
	Within string
	// OnError receives a directory that cannot be read, the root included,
	// and the error: nil skips the directory, an error ends the walk with
	// it. Without OnError the walk ends on the first such error.
	OnError func(path string, err error) error
}

// Files calls fn with the path of each regular file below root, or of root
// itself when it is one, in lexical order, ending at fn's first error. It
// follows symbolic links, entering a directory once however many paths lead to
// it, so a link cycle ends; a link to nothing, or to something other than a
// file or directory, is passed over. A path keeps the spelling it was reached
// by, links included.
func Files(root string, o Options, fn func(path string) error) error {
	w := walker{o: o, seen: map[string]bool{}, fn: fn}
	if o.Within != "" {
		within, err := filepath.EvalSymlinks(o.Within)
		if err != nil {
			return w.fail(root, err)
		}
		w.within = within
	}
	info, err := os.Stat(root)
	if err != nil {
		return w.fail(root, err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return w.fail(root, err)
	}
	if !w.inside(resolved) {
		return nil
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return fn(root)
		}
		return nil
	}
	return w.dir(root, resolved)
}

// walker is the state of one [Files] call.
type walker struct {
	o      Options
	within string
	// seen holds the real path of every directory entered.
	seen map[string]bool
	fn   func(path string) error
}

// dir visits the directory at path, whose real path is resolved.
func (w *walker) dir(path, resolved string) error {
	if w.seen[resolved] {
		return nil
	}
	w.seen[resolved] = true
	entries, err := os.ReadDir(path)
	if err != nil {
		return w.fail(path, err)
	}
	for _, e := range entries {
		if err := w.entry(path, resolved, e); err != nil {
			return err
		}
	}
	return nil
}

// entry visits e, an entry of the directory at path whose real path is resolved.
func (w *walker) entry(path, resolved string, e fs.DirEntry) error {
	name := e.Name()
	full := filepath.Join(path, name)
	mode := e.Type()
	childResolved := filepath.Join(resolved, name)
	if mode&fs.ModeSymlink != 0 {
		info, err := os.Stat(full)
		if err != nil {
			return nil
		}
		mode = info.Mode().Type()
		if childResolved, err = filepath.EvalSymlinks(full); err != nil {
			return nil
		}
	}
	switch {
	case mode.IsDir():
		if w.o.SkipDir != nil && w.o.SkipDir(name) || !w.inside(childResolved) {
			return nil
		}
		return w.dir(full, childResolved)
	case mode.IsRegular():
		if w.o.SkipFile != nil && w.o.SkipFile(name) {
			return nil
		}
		return w.fn(full)
	}
	return nil
}

// inside reports whether resolved, a real path, lies within the walk's bound.
func (w *walker) inside(resolved string) bool {
	return w.within == "" || resolved == w.within || strings.HasPrefix(resolved, w.within+string(filepath.Separator))
}

// fail hands an unreadable path to OnError.
func (w *walker) fail(path string, err error) error {
	if w.o.OnError == nil {
		return err
	}
	return w.o.OnError(path, err)
}

// SkipDesignDir reports a directory craftgo reads no design file from: a
// hidden one, vendor and node_modules.
func SkipDesignDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules"
}

// SkipDesignFile reports a file craftgo never reads as a design file: one
// whose name starts with . or _, as the go command ignores them, which covers
// an Emacs lock file (.#x.craftgo) and a macOS AppleDouble file (._x.craftgo).
func SkipDesignFile(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Design is the walk craftgo reads a design folder with: it follows links,
// skips what [SkipDesignDir] and [SkipDesignFile] name, and hands an
// unreadable directory to onErr.
func Design(onErr func(path string, err error) error) Options {
	return Options{SkipDir: SkipDesignDir, SkipFile: SkipDesignFile, OnError: onErr}
}
