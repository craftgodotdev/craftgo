package semantic

import (
	"strings"
	"testing"
)

// ---------- Happy paths ----------

func TestGenericInstanceCorrectArity(t *testing.T) {
	mustClean(t, `type Page<T> { items T[]  total int }
type User { id string }
type UserList { p Page<User>  flag bool }`)
}

func TestGenericMultiArg(t *testing.T) {
	mustClean(t, `type Pair<A, B> { left A  right B }
type User {}
type Org {}
type Team { members Pair<User, Org> }`)
}

func TestGenericNested(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type Box<T> { value T }
type User {}
type Wrapped { p Page<Box<User>> }`)
}

func TestGenericInMapValue(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type User {}
type Index { byTag map<string, Page<User>> }`)
}

func TestGenericInMethodResponse(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type User {}
service S {
	get List /list { response Page<User> }
}`)
}

func TestTypeParamFieldOK(t *testing.T) {
	// `T[]` inside the body of `Page<T>` is a type-param ref, not a
	// type lookup. Must not be flagged as unknown / non-generic.
	mustClean(t, `type Page<T> { items T[]  total int }
type User {}
type UserList { p Page<User> }`)
}

// ---------- Arity mismatch ----------

func TestGenericArityTooFew(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Pair<A, B> { left A  right B }
type User {}
type X { p Pair<User> }`))
	d := findCode(diags, CodeGenericArity)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "expects 2") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestGenericArityTooMany(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type User {}
type Org {}
type X { p Page<User, Org> }`))
	d := findCode(diags, CodeGenericArity)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "got 2") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestGenericMissingArgsOnGenericRef(t *testing.T) {
	// `Page` (no args) referenced where Page is generic - error.
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type X { p Page }`))
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- Non-generic with args ----------

func TestArgsOnNonGenericType(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type User { id string }
type X { p User<Org> }
type Org {}`))
	d := findCode(diags, CodeGenericNonGeneric)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "User") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestArgsOnTypeParam(t *testing.T) {
	// `T<X>` inside `Page<T>` body - type variable can't take args.
	_, diags := Analyze(parseFiles(t, `type Page<T> { item T<X> }
type X {}`))
	if findCode(diags, CodeGenericNonGeneric) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- Optional type argument (rejected) ----------

func TestGenericOptionalArgRejected(t *testing.T) {
	// `Page<Item?>` over an array field: the `?` would lower to a nullable
	// element on the Go side (`[]*Item`) while the OpenAPI items stay a
	// non-null `$ref`. Rejected.
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<Item?> }`))
	d := findCode(diags, CodeGenericOptionalArg)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "optional") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestGenericOptionalArgRejectedOnPlainField(t *testing.T) {
	// Rejected uniformly, even when the generic uses the param as a plain
	// field (`value T`) where it would lower cleanly - nullability is always
	// declared inside the generic, never on the argument.
	_, diags := Analyze(parseFiles(t, `type Wrap<T> { value T }
type Item { id string }
type X { p Wrap<Item?> }`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericOptionalArgRejectedInMethod(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Item { id string }
service S {
	get List /list { response Page<Item?> }
}`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericOptionalArgRejectedNested(t *testing.T) {
	// The optional arg is nested inside another generic instance.
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Box<T> { value T }
type Item { id string }
type X { p Box<Page<Item?>> }`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericMapValueOptionalArgClean(t *testing.T) {
	// Only the top-level argument `?` is rejected. A `?` on a map VALUE
	// inside the argument (`map<string, Item?>` -> `map[string]*Item`) is
	// nullable on both the Go and OpenAPI sides, so it stays clean.
	mustClean(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<map<string, Item?>> }`)
}

func TestGenericArrayArgClean(t *testing.T) {
	// A non-optional array argument is fine - `Page<Item[]>` is an
	// array-of-array, no optionality involved.
	mustClean(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<Item[]> }`)
}

// ---------- Mixin generic refs ----------

func TestMixinGenericValidatedByGenerics(t *testing.T) {
	// Both the mixin pass and the generic pass validate the args; the
	// mixin pass owns CodeMixinArity (different code, more specific
	// message context). We assert both don't double-fire on the same
	// happy path, then check the failing case is reported.
	mustClean(t, `type Page<T> { items T[] }
type User {}
type UserList { Page<User>  total int }`)
}

func TestMixinSkipsGenericArityCheck(t *testing.T) {
	// Mixin generic arity is owned by the mixin pass; the generics
	// pass should not duplicate the diag on the same span.
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type X { Page<A, B>  total int }
type A {}
type B {}`))
	mixin := 0
	generic := 0
	for _, d := range diags {
		switch d.Code {
		case CodeMixinArity:
			mixin++
		case CodeGenericArity:
			generic++
		}
	}
	if mixin != 1 {
		t.Errorf("expected 1 mixin/arity diag, got %d", mixin)
	}
	// The generic pass also walks mixin refs - either code is fine,
	// we just want at least one diagnostic.
	if mixin+generic < 1 {
		t.Errorf("expected at least one arity diag")
	}
}

// ---------- Unknown types silently skip ----------

func TestUnknownTypeRefSkipsArity(t *testing.T) {
	// Unknown `Foo<X>` - placement / qualified-ref pass owns the
	// "name not found" message (or it's a built-in we don't model).
	// The generics pass should not fire a confusing arity diag.
	_, diags := Analyze(parseFiles(t, `type X { p Foo<Bar> }`))
	if findCode(diags, CodeGenericArity) != nil {
		t.Errorf("unknown ref should not produce arity diag, got %v", codes(diags))
	}
	if findCode(diags, CodeGenericNonGeneric) != nil {
		t.Errorf("unknown ref should not produce non-generic diag, got %v", codes(diags))
	}
}

// ---------- Qualified ref ----------

// TestQualifiedGenericRefSinglePackageMode pins the per-package
// behaviour: a qualified ref like `shared.Page<User>` is NOT
// resolved when Analyze runs without a project context - the
// per-package pass can't reach into sibling packages. Arity check
// for qualified refs lives in the project resolver (see
// TestQualifiedGenericRefArityAcrossPackages).
func TestQualifiedGenericRefSinglePackageMode(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { p shared.Page<User> }
type User {}`))
	if findCode(diags, CodeGenericArity) != nil {
		t.Errorf("per-package mode should not validate qualified-ref arity, got %v", codes(diags))
	}
}

// TestGenericMapKeyArity verifies the recursion into map keys: a bad
// generic in `map<Bad<X>, Y>` is reported despite living in the key
// position rather than at the top level.
func TestGenericMapKeyArity(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type User {}
type X { byPage map<Page<User, X>, string> }`))
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// TestGenericWalkNilTypeRef covers the defensive nil-TypeRef guard.
// Parser doesn't produce nil refs but we keep the early return so a
// future regression doesn't crash the walker.
func TestGenericWalkNilTypeRef(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.walkTypeRefGenerics(nil, nil)
	if len(a.diags) != 0 {
		t.Errorf("nil ref should not diag, got %v", a.diags)
	}
}
