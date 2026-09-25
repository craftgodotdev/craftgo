package lsp

import (
	"path/filepath"
	"testing"
)

// A bare name resolves as the analyser resolves it: a type in its own
// package only, a middleware in any package.
func TestDefinitionOfABareNameFollowsTheAnalyser(t *testing.T) {
	design := designProject(t, map[string]string{
		"b/b.craftgo": "package b\n\ntype Foo { y int }\nmiddleware Auth\n",
	})
	path := filepath.Join(design, "a", "a.craftgo")
	if locs := definitionAt(t, path, "package a\n\ntype H { x Fo|o }\n"); len(locs) != 0 {
		t.Errorf("bare Foo is unknown in package a, yet definition answers %+v", locs)
	}
	locs := definitionAt(t, path, "package a\n\nservice S {\n\t@middlewares(Au|th)\n\tget G /g {}\n}\n")
	if len(locs) != 1 || filepath.Base(uriToPath(string(locs[0].URI))) != "b.craftgo" {
		t.Errorf("bare Auth resolves to %+v, want the middleware of package b", locs)
	}
}
