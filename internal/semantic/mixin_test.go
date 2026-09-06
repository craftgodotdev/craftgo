package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// ---------- Happy paths ----------

func TestMixinBasic(t *testing.T) {
	mustClean(t, `type Profile { id string }
type User { Profile  name string }`)
}

func TestMixinNested(t *testing.T) {
	// User → Profile → Auditable; field names cascade up cleanly.
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
	// A field whose Go field-name equals an embedded mixin's type name
	// collides with the generated struct embed (`Pagination` embed +
	// `pagination` field → both become `Pagination` → redeclared).
	d := expectDiag(t, `type Pagination { page int }
type Host { Pagination  pagination int }`, CodeMixinConflict)
	expectMessage(t, d, "collides with the embedded mixin")
}

func TestMixinMultiple(t *testing.T) {
	mustClean(t, `type Auditable { createdAt string }
type Identified { id string }
type User { Auditable  Identified  name string }`)
}

// A direct field and a mixin-promoted field whose DSL names differ but whose
// Go identifiers collide (`retryAfter` + promoted `retry_after` → both
// `RetryAfter`) land in separate Go structs that field-promotion merges by
// name, so the binder/validator/writers can't tell them apart. Reject.
func TestMixinPromotedGoNameCollidesWithHostField(t *testing.T) {
	d := expectDiag(t, `type HdrMix { retry_after int }
type Req { HdrMix  retryAfter int }`, CodeMixinConflict)
	expectMessage(t, d, "lower to the Go field")
}

// Two mixins each promoting a field that lowers to the same Go name land in
// two equal-depth embeds → ambiguous selector. Reject.
func TestMixinTwoPromotedGoNameCollision(t *testing.T) {
	d := expectDiag(t, `type A { userId string }
type B { user_id string }
type C { A  B }`, CodeMixinConflict)
	expectMessage(t, d, "lower to the Go field")
}

// Control: two DIRECT fields of one struct that collide on Go name are
// dedup-renamed by codegen (UserID / UserID_2) - the analyzer raises an
// informational warning but must NOT raise a mixin-conflict ERROR, since only
// the cross-embed case is unfixable.
func TestMixinDirectGoNameCollisionNotRejected(t *testing.T) {
	expectNoCode(t, `type R { userId string  user_id string }`, CodeMixinConflict)
}

func TestMixinInsideErrorDecl(t *testing.T) {
	mustClean(t, `type Auditable { createdAt string }
error BadRequest E { Auditable  details string }`)
}

// ---------- Unresolved / non-type ----------

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

// ---------- Cycle ----------

func TestMixinSelfCycle(t *testing.T) {
	expectDiag(t, `type A { A  name string }`, CodeMixinCycle)
}

func TestMixinIndirectCycle(t *testing.T) {
	expectDiag(t, `type A { B  a string }
type B { A  b string }`, CodeMixinCycle)
}

// ---------- Conflict ----------

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

// ---------- Generic mixin arity ----------

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

// ---------- Qualified skip ----------

// TestMixinDiamondSameTopLevel exercises the prev.from == sourceLabel
// branch directly: when a single top-level mixin reaches the same
// nested type via two internal paths, the duplicate field surfaces
// with the same source label and must be silently deduped (else the
// outer host would inherit a phantom conflict for every diamond in
// any sub-graph).
//
// We can't express this end-to-end in DSL because the intermediate
// "Combined" type itself has a real two-mixin diamond and is
// rightly reported. So we drive collectMixinFields directly with a
// hand-built AST that simulates expansion AT the outer host: a
// single sourceLabel walking two paths to the same field name.
func TestMixinDiamondSameTopLevel(t *testing.T) {
	a := newTestAnalyzer(&Package{
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
	})
	// Walk Combined as if it were the top-level mixin of an outer host
	// - sourceLabel stays "Combined" for both nested Base visits.
	seen := map[string]fieldOrigin{}
	a.collectMixinFields("", "Combined", "Combined", lexer.Position{Line: 1},
		seen, map[string]bool{".Outer": true})
	if len(a.diags) != 0 {
		t.Errorf("same-source diamond should not diag, got %v", a.diags)
	}
	if _, ok := seen["id"]; !ok {
		t.Errorf("expected `id` to be collected once, got %v", seen)
	}
}

// A qualified mixin whose package does not exist is reported once, as an
// unknown package, whether it sits on the host or inside a nested mixin.
func TestMixinNestedQualifiedUnknownPackage(t *testing.T) {
	expectDiag(t, `type Inner { shared.Other  id string }
type X { Inner  name string }`, CodeRefUnknownPackage)
}

func TestMixinQualifiedUnknownPackage(t *testing.T) {
	expectDiag(t, `type X { shared.Profile  name string }`, CodeRefUnknownPackage)
}

// TestMixinNilRefTolerated covers the defensive nil-ref / nil-Name
// guards in [analyzer.processMixin]. Parser doesn't emit these
// shapes today; the guard is for future regressions.
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

// TestMixinCollectMissingTarget exercises the "td not in pkg.Types"
// branch of collectMixinFields: nested mixin name resolves to an
// unknown type, walker silently bails out (top-level resolveMixinTarget
// already produced a diag).
func TestMixinCollectMissingTarget(t *testing.T) {
	a := newTestAnalyzer(&Package{
		Types: map[string]*ast.TypeDecl{},
	})
	a.collectMixinFields("", "Missing", "Missing", lexer.Position{Line: 1},
		map[string]fieldOrigin{}, map[string]bool{})
	if len(a.diags) != 0 {
		t.Errorf("missing nested mixin should not diag here, got %v", a.diags)
	}
}

// A mixin embedded twice in one type body lowers to a Go struct that
// declares the embedded type twice ("X redeclared") - rejected at design
// time rather than shipped as non-compiling code.
func TestDuplicateMixinEmbedRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Leaf { x string @minLength(1) }
type Req { Leaf  Leaf  r string }`))
	d := findCode(diags, CodeMixinConflict)
	if d == nil {
		t.Fatalf("expected duplicate-embed rejection; got %v", codes(diags))
	}
}

// A local mixin and an imported one whose unqualified names match both
// embed as the same Go field - rejected (would "redeclare").
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

// Two DIFFERENT types each embedding a mixin of the same name is fine.
func TestSameMixinNameDifferentTypesClean(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Leaf { x int }
type A { Leaf }
type B { Leaf }`))
	if findCode(diags, CodeMixinConflict) != nil {
		t.Errorf("same mixin in different types must be clean; got %v", codes(diags))
	}
}

// A mixin embedding a bare type-parameter of the host generic
// (`type Box<T> { T }`) is rejected - Go forbids embedding a type parameter,
// so the generated struct would never compile.
func TestTypeParamMixinRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Box<T> { T  note string }
type R { b Box<string> }`))
	if findCode(diags, CodeMixinConflict) == nil {
		t.Fatalf("expected type-param mixin rejection; got %v", codes(diags))
	}
	// project mode (gen path) must reject it too
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

// A `value T` named field (not an embed) must NOT be rejected - the control.
func TestTypeParamNamedFieldClean(t *testing.T) {
	mustClean(t, `type Box<T> { value T  note string }
type R { b Box<string> }`)
}
