package format

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormatDesignCorpus pins that every committed design file, in the examples
// and the e2e matrix, formats without diagnostics and idempotently.
func TestFormatDesignCorpus(t *testing.T) {
	repo, files := designCorpus(t)
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			src, err := fs.ReadFile(repo, name)
			if err != nil {
				t.Fatal(err)
			}
			once, diags := Format(name, string(src))
			if len(diags) > 0 {
				t.Fatalf("format produced diagnostics: %v", diags)
			}
			twice, diags := Format(name, once)
			if len(diags) > 0 {
				t.Fatalf("re-format produced diagnostics: %v\nformatted:\n%s", diags, once)
			}
			if once != twice {
				t.Error("format is not idempotent")
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
