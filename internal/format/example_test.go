package format

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormatDesignCorpus pins that every committed design file, in the examples
// and the e2e matrix, is in canonical form: it formats to itself without
// diagnostics.
func TestFormatDesignCorpus(t *testing.T) {
	repo, files := designCorpus(t)
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			src, err := fs.ReadFile(repo, name)
			if err != nil {
				t.Fatal(err)
			}
			out, diags := Format(name, string(src))
			if len(diags) > 0 {
				t.Fatalf("format produced diagnostics: %v", diags)
			}
			if out != string(src) {
				got, want := firstDifference(string(src), out)
				t.Errorf("not in canonical form (run craftgo fmt on it); first differing line:\n  file: %q\n  fmt:  %q", got, want)
			}
		})
	}
}

// firstDifference returns the first line on which a and b differ, from each.
func firstDifference(a, b string) (string, string) {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return al[i], bl[i]
		}
	}
	if len(al) > len(bl) {
		return al[len(bl)], ""
	}
	return "", bl[len(al)]
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
