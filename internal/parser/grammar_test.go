package parser

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseDesignCorpus pins that every committed design file, in the examples
// and the e2e matrix, parses without diagnostics.
func TestParseDesignCorpus(t *testing.T) {
	repo, files := designCorpus(t)
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			src, err := fs.ReadFile(repo, name)
			if err != nil {
				t.Fatal(err)
			}
			p := New(name, string(src))
			p.Parse()
			if diags := p.Diagnostics(); len(diags) > 0 {
				var msgs []string
				for _, d := range diags {
					msgs = append(msgs, d.Msg)
				}
				t.Errorf("parse diagnostics:\n  - %s", strings.Join(msgs, "\n  - "))
			}
		})
	}
}

// designCorpus returns the repository and the path of every .craftgo file under
// example/ and tests/e2e/matrix/design in it; a missing or empty root is fatal.
func designCorpus(t *testing.T) (fs.FS, []string) {
	t.Helper()
	repo := os.DirFS(filepath.Join("..", ".."))
	var files []string
	for _, root := range []string{"example", "tests/e2e/matrix/design"} {
		n := len(files)
		err := fs.WalkDir(repo, root, func(name string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(name, ".craftgo") {
				files = append(files, name)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == n {
			t.Fatalf("no .craftgo files under %s", root)
		}
	}
	return repo, files
}
