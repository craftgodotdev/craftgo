package docs

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// `@errors` follows the merge's rename of an error two packages declare.
func TestCrossPkgErrorNameCollisionFollowsRename(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"a/a.craftgo": `package a
error Conflict Dup { reason string }
service ASvc {
  @errors(Dup)
  post Make /a { request Mk }
}

type Mk { name string }`,
		"b/b.craftgo": `package b
import "a"
error Conflict Dup { reason string }
service BSvc {
  @errors(Dup)
  post MakeB /b { request MkB }
  @errors(a.Dup)
  post MakeC /c { request MkB }
}
type MkB { name string }`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	merged := mergeProjectForOpenAPI(proj)
	doc, err := buildOpenAPIDoc(merged, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(path, wantSuffix string) {
		item := doc.Paths.Find(path)
		if item == nil || item.Post == nil {
			t.Fatalf("no POST %s", path)
		}
		r409 := item.Post.Responses.Status(409)
		if r409 == nil || r409.Value == nil {
			t.Fatalf("%s: 409 @errors response dropped", path)
		}
		mt := r409.Value.Content.Get("application/json")
		if mt == nil || mt.Schema == nil || !strings.HasSuffix(mt.Schema.Ref, wantSuffix) {
			got := ""
			if mt != nil && mt.Schema != nil {
				got = mt.Schema.Ref
			}
			t.Fatalf("%s: 409 ref=%q, want suffix %q", path, got, wantSuffix)
		}
	}
	check("/a", "ADupErr") // a's own Dup, renamed
	check("/b", "BDupErr") // b's own Dup (bare), renamed
	check("/c", "ADupErr") // b -> a.Dup (qualified), follows a's rename
}
