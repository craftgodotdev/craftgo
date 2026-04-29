package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
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
	_, diags := Analyze(parseFiles(t, `type X { Mystery  name string }`))
	d := findCode(diags, CodeMixinUnresolved)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "Mystery") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestMixinOnEnum(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum Status { Active  Inactive }
type X { Status  name string }`))
	d := findCode(diags, CodeMixinNonType)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "enum") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestMixinOnError(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `error NotFound UserNotFound
type X { UserNotFound  name string }`))
	d := findCode(diags, CodeMixinNonType)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "error") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestMixinOnScalar(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `scalar Email string
type X { Email  name string }`))
	if findCode(diags, CodeMixinNonType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMixinOnMiddleware(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `middleware Auth
type X { Auth  name string }`))
	if findCode(diags, CodeMixinNonType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- Cycle ----------

func TestMixinSelfCycle(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type A { A  name string }`))
	if findCode(diags, CodeMixinCycle) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMixinIndirectCycle(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type A { B  a string }
type B { A  b string }`))
	d := findCode(diags, CodeMixinCycle)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- Conflict ----------

func TestMixinConflictHostVsMixin(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Profile { id string }
type User { Profile  id int }`))
	d := findCode(diags, CodeMixinConflict)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, `"id"`) {
		t.Errorf("msg = %q", d.Msg)
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to first declaration, got %+v", d.Related)
	}
}

func TestMixinConflictMixinVsMixin(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type A { name string }
type B { name int }
type X { A  B }`))
	if findCode(diags, CodeMixinConflict) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMixinNoConflictDifferentNames(t *testing.T) {
	mustClean(t, `type A { id string }
type B { name string }
type X { A  B  email string }`)
}

// ---------- Generic mixin arity ----------

func TestMixinGenericArityMismatch(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type UserList { Page<User, Org>  total int }
type User {}
type Org {}`))
	d := findCode(diags, CodeMixinArity)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "expects 1") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestMixinGenericMissingArgs(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type UserList { Page  total int }`))
	if findCode(diags, CodeMixinArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestMixinGenericArgsOnNonGeneric(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Profile { id string }
type User { Profile<X>  name string }`))
	if findCode(diags, CodeMixinArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
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
	// — sourceLabel stays "Combined" for both nested Base visits.
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
	// a qualified mixin `shared.Other` — the qualified-ref pass handles
	// the user-facing report; collectMixinFields skips silently.
	mustClean(t, `type Inner { shared.Other  id string }
type X { Inner  name string }`)
}

func TestMixinQualifiedSkipped(t *testing.T) {
	// Qualified mixin (`shared.Profile`) — codegen takes the trailing
	// segment, so the mixin pass intentionally skips. The qualified-ref
	// pass also exempts mixins (see [analyzer.checkQualifiedRefs]), so
	// neither diagnostic fires.
	_, diags := Analyze(parseFiles(t, `type X { shared.Profile  name string }`))
	if findCode(diags, CodeMixinUnresolved) != nil {
		t.Errorf("mixin pass should skip qualified refs, got %v", codes(diags))
	}
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
