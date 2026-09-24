package semantic

import (
	"slices"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func TestMixinBasic(t *testing.T) {
	mustClean(t, `type Profile { id string }
type User { Profile  name string }`)
}

func TestMixinNested(t *testing.T) {
	mustClean(t, `type Auditable { createdAt string  updatedAt string }
type Profile { Auditable  id string }
type User { Profile  name string }`)
}

func TestMixinGeneric(t *testing.T) {
	mustClean(t, `type Page<T> { items T[]  total int }
type UserList { Page<User>  requestId string }
type User { id string }`)
}

func TestMixinFieldEmbedNameCollision(t *testing.T) {
	// Field `pagination` and the embedded `Pagination` share a Go name.
	d := expectDiag(t, `type Pagination { page int }
type Host { Pagination  pagination int }`, CodeMixinConflict)
	expectMessage(t, d, "collides with the embedded mixin")
}

func TestMixinMultiple(t *testing.T) {
	mustClean(t, `type Auditable { createdAt string }
type Identified { id string }
type User { Auditable  Identified  name string }`)
}

// A host field and a promoted field with one Go name (`retryAfter`, `retry_after`) conflict.
func TestMixinPromotedGoNameCollidesWithHostField(t *testing.T) {
	d := expectDiag(t, `type HdrMix { retry_after int }
type Req { HdrMix  retryAfter int }`, CodeMixinConflict)
	expectMessage(t, d, "lower to the Go field")
}

// Two mixins promoting fields with one Go name conflict.
func TestMixinTwoPromotedGoNameCollision(t *testing.T) {
	d := expectDiag(t, `type A { userId string }
type B { user_id string }
type C { A  B }`, CodeMixinConflict)
	expectMessage(t, d, "lower to the Go field")
}

// Two direct fields with one Go name are not a mixin conflict.
func TestMixinDirectGoNameCollisionNotRejected(t *testing.T) {
	expectNoCode(t, `type R { userId string  user_id string }`, CodeMixinConflict)
}

func TestMixinInsideErrorDecl(t *testing.T) {
	mustClean(t, `type Auditable { createdAt string }
error BadRequest E { Auditable  details string }`)
}

func TestMixinUnresolved(t *testing.T) {
	d := expectDiag(t, `type X { Mystery  name string }`, CodeRefUnknownSymbol)
	expectMessage(t, d, "Mystery")
}

func TestMixinOnEnum(t *testing.T) {
	d := expectDiag(t, `enum Status { Active  Inactive }
type X { Status  name string }`, CodeMixinNonType)
	expectMessage(t, d, "enum")
}

func TestMixinOnError(t *testing.T) {
	d := expectDiag(t, `error NotFound UserNotFound
type X { UserNotFound  name string }`, CodeMixinNonType)
	expectMessage(t, d, "error")
}

func TestMixinOnScalar(t *testing.T) {
	expectDiag(t, `scalar Email string
type X { Email  name string }`, CodeMixinNonType)
}

func TestMixinOnMiddleware(t *testing.T) {
	expectDiag(t, `middleware Auth
type X { Auth  name string }`, CodeMixinNonType)
}

// A mixin naming an error or a middleware gets the mixin diagnostic alone.
func TestMixinOnNonTypeReportedOnce(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `error NotFound Gone
middleware Auth
type X { Gone  Auth  name string }`))
	if got := codes(diags); !slices.Equal(got, []string{CodeMixinNonType, CodeMixinNonType}) {
		t.Errorf("want two %s, got %v", CodeMixinNonType, diags)
	}
}

func TestMixinSelfCycle(t *testing.T) {
	expectDiag(t, `type A { A  name string }`, CodeMixinCycle)
}

func TestMixinIndirectCycle(t *testing.T) {
	expectDiag(t, `type A { B  a string }
type B { A  b string }`, CodeMixinCycle)
}

func TestMixinConflictHostVsMixin(t *testing.T) {
	d := expectDiag(t, `type Profile { id string }
type User { Profile  id int }`, CodeMixinConflict)
	expectMessage(t, d, `"id"`)
	if len(d.Related) != 1 {
		t.Errorf("expected related to first declaration, got %+v", d.Related)
	}
}

func TestMixinConflictMixinVsMixin(t *testing.T) {
	expectDiag(t, `type A { name string }
type B { name int }
type X { A  B }`, CodeMixinConflict)
}

func TestMixinNoConflictDifferentNames(t *testing.T) {
	mustClean(t, `type A { id string }
type B { name string }
type X { A  B  email string }`)
}

func TestMixinGenericArityMismatch(t *testing.T) {
	d := expectDiag(t, `type Page<T> { items T[] }
type UserList { Page<User, Org>  total int }
type User {}
type Org {}`, CodeMixinArity)
	expectMessage(t, d, "expects 1")
}

func TestMixinGenericMissingArgs(t *testing.T) {
	expectDiag(t, `type Page<T> { items T[] }
type UserList { Page  total int }`, CodeMixinArity)
}

func TestMixinGenericArgsOnNonGeneric(t *testing.T) {
	expectDiag(t, `type Profile { id string }
type User { Profile<X>  name string }`, CodeMixinArity)
}

// collectMixinFields collects a field once when one top-level mixin reaches it by two paths.
func TestMixinDiamondSameTopLevel(t *testing.T) {
	pkg := &Package{
		Types: map[string]*ast.TypeDecl{
			"Base": {
				Name: "Base",
				Body: []ast.TypeMember{
					&ast.Field{Name: "id", Pos: lexer.Position{Line: 1}},
				},
			},
			"Combined": {
				Name: "Combined",
				Body: []ast.TypeMember{
					&ast.Mixin{Pos: lexer.Position{Line: 2}, Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Base"}}}},
					&ast.Mixin{Pos: lexer.Position{Line: 3}, Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Base"}}}},
				},
			},
		},
	}
	a := newTestAnalyzer(pkg)
	// Walk Combined as the top-level mixin of an outer host.
	seen := map[string]fieldOrigin{}
	a.collectMixinFields(pkg, "Combined", "Combined", lexer.Position{Line: 1},
		seen, map[string]bool{".Outer": true})
	if len(a.diags) != 0 {
		t.Errorf("same-source diamond should not diag, got %v", a.diags)
	}
	if _, ok := seen["id"]; !ok {
		t.Errorf("expected `id` to be collected once, got %v", seen)
	}
}

// A nested mixin from an unknown package is reported as an unknown package.
func TestMixinNestedQualifiedUnknownPackage(t *testing.T) {
	expectDiag(t, `type Inner { shared.Other  id string }
type X { Inner  name string }`, CodeRefUnknownPackage)
}

func TestMixinQualifiedUnknownPackage(t *testing.T) {
	expectDiag(t, `type X { shared.Profile  name string }`, CodeRefUnknownPackage)
}

// processMixin skips a mixin with a nil ref or name.
func TestMixinNilRefTolerated(t *testing.T) {
	a := newTestAnalyzer(&Package{
		Types: map[string]*ast.TypeDecl{},
	})
	a.processMixin("X", &ast.Mixin{Pos: lexer.Position{Line: 1}, Ref: nil}, map[string]fieldOrigin{})
	a.processMixin("X", &ast.Mixin{Pos: lexer.Position{Line: 1}, Ref: &ast.NamedTypeRef{}}, map[string]fieldOrigin{})
	if len(a.diags) != 0 {
		t.Errorf("nil ref should not diag, got %v", a.diags)
	}
}

// collectMixinFields skips an unknown mixin without a diagnostic.
func TestMixinCollectMissingTarget(t *testing.T) {
	pkg := &Package{Types: map[string]*ast.TypeDecl{}}
	a := newTestAnalyzer(pkg)
	a.collectMixinFields(pkg, "Missing", "Missing", lexer.Position{Line: 1},
		map[string]fieldOrigin{}, map[string]bool{})
	if len(a.diags) != 0 {
		t.Errorf("missing nested mixin should not diag here, got %v", a.diags)
	}
}

// A mixin embedded twice in one type is rejected.
func TestDuplicateMixinEmbedRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Leaf { x string @minLength(1) }
type Req { Leaf  Leaf  r string }`))
	d := findCode(diags, CodeMixinConflict)
	if d == nil {
		t.Fatalf("expected duplicate-embed rejection; got %v", codes(diags))
	}
}

// A local and an imported mixin of one name conflict; both embed as the same Go field.
func TestLeafNameEmbedCollisionRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Leaf { x int }`,
		"api.craftgo": `package design
import "shared"
type Leaf { y int }
type Req { Leaf  shared.Leaf  r string }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMixinConflict) == nil {
		t.Fatalf("expected leaf-name embed collision; got %v", codes(diags))
	}
}

// Two types may embed the same mixin.
func TestSameMixinNameDifferentTypesClean(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Leaf { x int }
type A { Leaf }
type B { Leaf }`))
	if findCode(diags, CodeMixinConflict) != nil {
		t.Errorf("same mixin in different types must be clean; got %v", codes(diags))
	}
}

// Embedding a type parameter (`type Box<T> { T }`) is rejected, in package and project analysis.
func TestTypeParamMixinRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Box<T> { T  note string }
type R { b Box<string> }`))
	if findCode(diags, CodeMixinConflict) == nil {
		t.Fatalf("expected type-param mixin rejection; got %v", codes(diags))
	}
	root, files := projectFixture(t, map[string]string{
		"api.craftgo": `package design
type Box<T> { T  note string }
type R { b Box<string> }`,
	})
	_, pdiags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(pdiags, CodeMixinConflict) == nil {
		t.Fatalf("expected type-param mixin rejection in project mode; got %v", codes(pdiags))
	}
}

// A named field of a type-parameter type is accepted.
func TestTypeParamNamedFieldClean(t *testing.T) {
	mustClean(t, `type Box<T> { value T  note string }
type R { b Box<string> }`)
}
