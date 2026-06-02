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

func TestMixinInsideErrorDecl(t *testing.T) {
	mustClean(t, `type Auditable { createdAt string }
error BadRequest E { Auditable  details string }`)
}

// ---------- Unresolved / non-type ----------

func TestMixinUnresolved(t *testing.T) {
	d := expectDiag(t, `type X { Mystery  name string }`, CodeMixinUnresolved)
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
	a := &analyzer{pkg: &Package{
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
	}}
	// Walk Combined as if it were the top-level mixin of an outer host
	// - sourceLabel stays "Combined" for both nested Base visits.
	seen := map[string]fieldOrigin{}
	a.collectMixinFields("Combined", "Combined", lexer.Position{Line: 1},
		seen, map[string]bool{"Outer": true})
	if len(a.diags) != 0 {
		t.Errorf("same-source diamond should not diag, got %v", a.diags)
	}
	if _, ok := seen["id"]; !ok {
		t.Errorf("expected `id` to be collected once, got %v", seen)
	}
}

// TestMixinNestedQualifiedSkipped covers the nested-mixin defensive
// guard: a qualified ref inside a mixin's body is silently skipped
// rather than crashing the walker.
func TestMixinNestedQualifiedSkipped(t *testing.T) {
	// Top-level Mixin "Inner" is unqualified, but inside Inner there's
	// a qualified mixin `shared.Other` - the qualified-ref pass handles
	// the user-facing report; collectMixinFields skips silently.
	mustClean(t, `type Inner { shared.Other  id string }
type X { Inner  name string }`)
}

func TestMixinQualifiedSkipped(t *testing.T) {
	// Qualified mixin (`shared.Profile`) - codegen takes the trailing
	// segment, so the mixin pass intentionally skips. The qualified-ref
	// pass also exempts mixins (see [analyzer.checkQualifiedRefs]), so
	// neither diagnostic fires.
	expectNoCode(t, `type X { shared.Profile  name string }`, CodeMixinUnresolved)
}

// TestMixinNilRefTolerated covers the defensive nil-ref / nil-Name
// guards in [analyzer.processMixin]. Parser doesn't emit these
// shapes today; the guard is for future regressions.
func TestMixinNilRefTolerated(t *testing.T) {
	a := &analyzer{pkg: &Package{
		Types: map[string]*ast.TypeDecl{},
	}}
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
	a := &analyzer{pkg: &Package{
		Types: map[string]*ast.TypeDecl{},
	}}
	a.collectMixinFields("Missing", "Missing", lexer.Position{Line: 1},
		map[string]fieldOrigin{}, map[string]bool{})
	if len(a.diags) != 0 {
		t.Errorf("missing nested mixin should not diag here, got %v", a.diags)
	}
}
