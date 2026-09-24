package protodesign

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// discover lists every `.proto` under designRoot as a sorted slash path
// relative to it; a directory it cannot read fails the call.
func discover(designRoot string) ([]string, error) {
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
