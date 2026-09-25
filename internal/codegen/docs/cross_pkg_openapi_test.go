package docs

import (
	"slices"
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
	doc, err := buildProjectDocument(proj, &config.Config{})
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

// `@errors` names the error semantic resolves, in its array form too: a bare
// name declared in two other packages is the first package's.
func TestCrossPkgErrorRefsResolveLikeSemantic(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
error NotFound Dup { id string }
type Out { ok bool }
service S {
  @errors([Dup])
  get Own /own { response Out }
  @errors([b.Dup])
  get Qualified /qualified { response Out }
  @errors(Gone)
  get Elsewhere /elsewhere { response Out }
}`,
		"b/b.craftgo": `package b
error NotFound Dup { reason string }
error NotFound Gone { b string }`,
		"c/c.craftgo": `package c
error NotFound Gone { c string }`,
	}, &config.Config{})
	for path, want := range map[string]string{
		"/own":       "#/components/schemas/ADupErr",
		"/qualified": "#/components/schemas/BDupErr",
		"/elsewhere": "#/components/schemas/BGoneErr",
	} {
		r404 := doc.Paths.Find(path).Get.Responses.Status(404)
		if r404 == nil || r404.Value == nil {
			t.Errorf("%s: the @errors 404 response is missing", path)
			continue
		}
		if got := r404.Value.Content.Get("application/json").Schema.Ref; got != want {
			t.Errorf("%s: 404 refs %q, want %q", path, got, want)
		}
	}
}

// Services two packages name alike are each documented with their own
// prefix, tags and group.
func TestCrossPkgSameNamedServicesAreAllDocumented(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type R { ok bool }
@prefix("/pa")
@tags(alpha)
@group("x")
service S { get One /one { response R } }`,
		"b/b.craftgo": `package b
type Q { n int }
@tags(beta)
@group("y")
service S { get Two /two { response Q } }`,
	}, &config.Config{})
	for path, want := range map[string][]string{
		"/pa/one": {"alpha", "x"},
		"/two":    {"beta", "y"},
	} {
		item := doc.Paths.Find(path)
		if item == nil || item.Get == nil {
			t.Errorf("GET %s is not documented", path)
			continue
		}
		if !slices.Equal(item.Get.Tags, want) {
			t.Errorf("GET %s tags = %v, want %v", path, item.Get.Tags, want)
		}
	}
}

// Same-named services of two packages that share a method name keep their
// own body components, named after their package; the operationIds stay the
// analyser's.
func TestCrossPkgSharedMethodNamesKeepTheirOwnComponents(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type In { x string }
type Out { y string }
@prefix("/a")
@group("ga")
service S {
	@operationId("aGet")
	post Get /get { request In  response Out }
}`,
		"b/b.craftgo": `package b
type Req { z int }
type Resp { w int }
@prefix("/b")
@group("gb")
service S { post Get /get { request Req  response Resp } }`,
	}, &config.Config{})
	for path, want := range map[string]struct{ id, stem, field string }{
		"/a/get": {id: "aGet", stem: "ASGet", field: "x"},
		"/b/get": {id: "SGet", stem: "BSGet", field: "z"},
	} {
		op := doc.Paths.Find(path).Post
		if op.OperationID != want.id {
			t.Errorf("%s operationId = %q, want %q", path, op.OperationID, want.id)
		}
		if got := op.RequestBody.Value.Content.Get("application/json").Schema.Ref; got != "#/components/schemas/"+want.stem+"ReqBody" {
			t.Errorf("%s request body refs %q, want %sReqBody", path, got, want.stem)
		}
		if got := op.Responses.Status(201).Value.Content.Get("application/json").Schema.Ref; got != "#/components/schemas/"+want.stem+"RespBody" {
			t.Errorf("%s response refs %q, want %sRespBody", path, got, want.stem)
		}
		if body := doc.Components.Schemas[want.stem+"ReqBody"]; body == nil || body.Value.Properties[want.field] == nil {
			t.Errorf("%sReqBody does not hold %s's field %q", want.stem, path, want.field)
		}
	}
}

// A type parameter stays a parameter in the merge, even named like a type
// two packages declare.
func TestCrossPkgMergeKeepsTypeParameters(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type T { x int }
type Item { id string }
type Box<T> { inner T }
type Holder { box Box<Item> }
service S { get L /l { response Holder } }`,
		"b/b.craftgo": `package b
type T { y string }`,
	}, &config.Config{})
	inst := doc.Components.Schemas["BoxOfItem"]
	if inst == nil || inst.Value == nil {
		t.Fatal("no BoxOfItem component")
	}
	if got := inst.Value.Properties["inner"].Ref; got != "#/components/schemas/Item" {
		t.Errorf("BoxOfItem.inner refs %q, want the type argument Item", got)
	}
}
