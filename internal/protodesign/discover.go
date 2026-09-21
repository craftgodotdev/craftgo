package protodesign

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Discover lists every `.proto` under designRoot as a slash-separated
// path relative to it, sorted. Those are the names the compiler and the
// plugins see, so a proto imports its sibling by that same relative
// path: the design folder is the import root.
//
// A directory it cannot read fails the call: a generator must not
// generate from half a design.
func Discover(designRoot string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(designRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		rel, err := filepath.Rel(designRoot, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
