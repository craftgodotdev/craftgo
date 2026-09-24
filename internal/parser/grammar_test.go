package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseCornercaseFixtures pins that every cornercase design file parses
// without diagnostics.
func TestParseCornercaseFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "e2e", "cornercase", "design")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skipf("cornercase fixtures not present at %s", root)
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || filepath.Ext(path) != ".craftgo" {
			return walkErr
		}
		rel, _ := filepath.Rel(root, path)
		t.Run(rel, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			p := New(path, string(src))
			p.Parse()
			if diags := p.Diagnostics(); len(diags) > 0 {
				var msgs []string
				for _, d := range diags {
					msgs = append(msgs, d.Msg)
				}
				t.Errorf("parse diagnostics:\n  - %s", strings.Join(msgs, "\n  - "))
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
