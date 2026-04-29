package format

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFormatRealExamples runs the formatter against every .craftgo file in
// the bundled example/ tree. It is a lightweight smoke test: each file must
// (1) format without diagnostics, (2) be idempotent under reformat.
func TestFormatRealExamples(t *testing.T) {
	root, err := filepath.Abs("../../example/design")
	if err != nil {
		t.Skip("cannot resolve example path:", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Skip("example tree not present:", err)
	}
	var checked int
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".craftgo" {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out1, diags := Format(p, string(raw))
		if len(diags) > 0 {
			t.Errorf("%s: format produced diagnostics: %v", p, diags)
			return nil
		}
		out2, diags := Format(p, out1)
		if len(diags) > 0 {
			t.Errorf("%s: re-format produced diagnostics: %v\nformatted:\n%s", p, diags, out1)
			return nil
		}
		if out1 != out2 {
			t.Errorf("%s: not idempotent", p)
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checked %d example files", checked)
}
