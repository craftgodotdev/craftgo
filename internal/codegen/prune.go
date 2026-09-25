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

// sweepDir is a directory the sweep walks and the headers that mark a file in it as regenerated.
type sweepDir struct {
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

// owned lists dirs, a directory's headers from every target writing there, in path order,
// dropping the project root and its ancestors: the root may hold a sibling design's output, so it
// is never swept.
func owned(dirs map[string][]string, projectRoot string) []sweepDir {
	root := filepath.Clean(projectRoot)
	merged := map[string][]string{}
	for dir, headers := range dirs {
		dir = filepath.Clean(dir)
		if dir == root || strings.HasPrefix(root, dir+string(filepath.Separator)) {
			continue
		}
		merged[dir] = append(merged[dir], headers...)
	}
	out := make([]sweepDir, 0, len(merged))
	for _, dir := range slices.Sorted(maps.Keys(merged)) {
		headers := merged[dir]
		slices.Sort(headers)
		out = append(out, sweepDir{path: dir, headers: slices.Compact(headers)})
	}
	return out
}

// prune deletes, under each of dirs, the generated files written does not
// name, then the directories that leaves empty.
func prune(dirs []sweepDir, written map[string]bool) error {
	for _, dir := range dirs {
		root := dir.path
		var emptied []string
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || written[path] || !isGenerated(path, dir.headers) {
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
