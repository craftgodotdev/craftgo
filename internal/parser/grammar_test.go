package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseCornercaseFixtures walks every `.craftgo` file under the
// cornercase e2e fixture tree and asserts the parser accepts it
// without diagnostics. The cornercase corpus is the project's
// authoritative collection of "every shape the DSL ever supported";
// the codegen drift guard runs against it, so a parser regression
// would surface there sooner or later — but the smoke test in this
// file fires earlier (parse-time) with a focused failure label and
// no codegen overhead.
//
// Adding a new fixture under `tests/e2e/cornercase/design/` → new
// subtest automatically. No edit to this file needed.
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
