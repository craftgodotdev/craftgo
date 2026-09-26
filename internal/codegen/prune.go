package codegen

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// sweepPath is a path the sweep covers, a directory with everything under it or a single file,
// and the headers that mark a file there as regenerated.
type sweepPath struct {
	path    string
	headers []string
}

// isGenerated reports whether the file at path opens with one of headers;
// a missing or unreadable file does not.
func isGenerated(path string, headers []string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, header := range headers {
		if strings.HasPrefix(string(body), header) {
			return true
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

// prune deletes, at each of paths, the generated files written does not name, then the
// directories that leaves empty.
func prune(paths []sweepPath, written map[string]bool) error {
	for _, sp := range paths {
		root := sp.path
		var emptied []string
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || written[path] || !isGenerated(path, sp.headers) {
				return nil
			}
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				return rmErr
			}
			emptied = append(emptied, filepath.Dir(path))
			return nil
		})
		if err != nil {
			return err
		}
		pruneEmptyDirs(root, emptied)
	}
	return nil
}

// pruneEmptyDirs removes the emptied dirs, deepest first, and each parent
// that leaves empty, up to but not including root.
func pruneEmptyDirs(root string, dirs []string) {
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		for strings.HasPrefix(dir, root+string(filepath.Separator)) {
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
