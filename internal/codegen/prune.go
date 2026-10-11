package codegen

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/walk"
)

// sweepPath is a path the sweep covers, a directory with everything under it or a single file,
// and the headers that mark a file there as regenerated.
type sweepPath struct {
	path    string
	headers []string
}

// isGenerated reports whether one of headers is a line of the file at path before its first
// code, after any comment block such as the license header protoc-gen-go copies from a proto;
// a missing or unreadable file is not generated.
func isGenerated(path string, headers []string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(body)) {
		line = strings.TrimRight(line, "\r\n")
		if slices.Contains(headers, line) {
			return true
		}
		if line != "" && !strings.HasPrefix(line, "//") {
			return false
		}
	}
	return false
}

// owned lists paths, a path's headers from every target writing there, in path order, dropping
// the project root and its ancestors: the root may hold a sibling design's output, so it is never
// swept.
func owned(paths map[string][]string, projectRoot string) []sweepPath {
	root := filepath.Clean(projectRoot)
	merged := map[string][]string{}
	for p, headers := range paths {
		p = filepath.Clean(p)
		if p == root || strings.HasPrefix(root, p+string(filepath.Separator)) {
			continue
		}
		merged[p] = append(merged[p], headers...)
	}
	out := make([]sweepPath, 0, len(merged))
	for _, p := range slices.Sorted(maps.Keys(merged)) {
		headers := merged[p]
		slices.Sort(headers)
		out = append(out, sweepPath{path: p, headers: slices.Compact(headers)})
	}
	return out
}

// prune deletes, at each of paths, the generated files the run did not write, then the
// directories that leaves empty: below a swept directory, up to the project root for a swept
// file. It follows links to directories that stay inside the project root, and no other.
func prune(projectRoot string, paths []sweepPath, written map[string]bool) error {
	wrote := newWrittenSet(written)
	for _, sp := range paths {
		top := sp.path
		if info, err := os.Stat(sp.path); err == nil && !info.IsDir() {
			top = projectRoot
		}
		var emptied []string
		opts := walk.Options{Within: projectRoot, OnError: func(string, error) error { return nil }}
		err := walk.Files(sp.path, opts, func(path string) error {
			if wrote.has(path) || !isGenerated(path, sp.headers) {
				return nil
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			emptied = append(emptied, filepath.Dir(path))
			return nil
		})
		if err != nil {
			return err
		}
		pruneEmptyDirs(top, emptied)
	}
	return nil
}

// writtenSet is the files a run wrote.
type writtenSet struct {
	paths map[string]bool
	// byName holds the written paths by lower-cased file name, for [writtenSet.has] to meet a
	// file the sweep reaches under another spelling.
	byName map[string][]string
}

func newWrittenSet(written map[string]bool) writtenSet {
	s := writtenSet{paths: written, byName: map[string][]string{}}
	for p := range written {
		name := strings.ToLower(filepath.Base(p))
		s.byName[name] = append(s.byName[name], p)
	}
	return s
}

// has reports whether the run wrote the file at path: under that spelling, or under another one
// that names the same file - through a linked directory, or on a file system that folds case,
// where a directory renamed in case only keeps its old spelling on disk.
func (s writtenSet) has(path string) bool {
	if s.paths[path] {
		return true
	}
	for _, p := range s.byName[strings.ToLower(filepath.Base(path))] {
		if sameFile(p, path) {
			return true
		}
	}
	return false
}

// sameFile reports whether a and b name one existing file; a test stands in for a file system
// that folds case through it.
var sameFile = func(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// pruneEmptyDirs removes the emptied dirs, deepest first, and each parent
// that leaves empty, up to but not including root; a link is never removed.
func pruneEmptyDirs(root string, dirs []string) {
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		for strings.HasPrefix(dir, root+string(filepath.Separator)) {
			if info, err := os.Lstat(dir); err != nil || info.Mode()&fs.ModeSymlink != 0 {
				break
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) > 0 {
				break
			}
			if os.Remove(dir) != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
}
