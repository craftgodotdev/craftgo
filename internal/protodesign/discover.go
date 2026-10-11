package protodesign

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/walk"
)

// discover lists every `.proto` a [walk.Design] walk of designRoot finds, as a
// sorted slash path relative to it; a directory it cannot read fails the call.
func discover(designRoot string) ([]string, error) {
	var out []string
	err := walk.Files(designRoot, walk.Design(nil), func(path string) error {
		if !strings.HasSuffix(path, ".proto") {
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
