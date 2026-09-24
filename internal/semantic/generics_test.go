package semantic

import (
	"strings"
	"testing"
)

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
	mustClean(t, `type Page<T> { items T[]  total int }
type User {}
type UserList { p Page<User> }`)
}

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
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type X { p Page }`))
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

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
	// A type parameter takes no type arguments.
	_, diags := Analyze(parseFiles(t, `type Page<T> { item T<X> }
type X {}`))
	if findCode(diags, CodeGenericNonGeneric) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericOptionalArgRejected(t *testing.T) {
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
	// Nullability belongs inside the generic, never on its argument.
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
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type Box<T> { value T }
type Item { id string }
type X { p Box<Page<Item?>> }`))
	if findCode(diags, CodeGenericOptionalArg) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestGenericMapValueOptionalArgClean(t *testing.T) {
	// Only a `?` on the argument itself is rejected, not one on a map value inside it.
	mustClean(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<map<string, Item?>> }`)
}

func TestGenericArrayArgClean(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type Item { id string }
type X { p Page<Item[]> }`)
}

// A generic mixin with the right arity passes the mixin and generics checks.
func TestMixinGenericValidatedByGenerics(t *testing.T) {
	mustClean(t, `type Page<T> { items T[] }
type User {}
type UserList { Page<User>  total int }`)
}

// A generic mixin with the wrong arity reports one mixin arity diagnostic.
func TestMixinSkipsGenericArityCheck(t *testing.T) {
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
	if mixin+generic < 1 {
		t.Errorf("expected at least one arity diag")
	}
}

// The generics check skips an unknown type.
func TestUnknownTypeRefSkipsArity(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { p Foo<Bar> }`))
	if findCode(diags, CodeGenericArity) != nil {
		t.Errorf("unknown ref should not produce arity diag, got %v", codes(diags))
	}
	if findCode(diags, CodeGenericNonGeneric) != nil {
		t.Errorf("unknown ref should not produce non-generic diag, got %v", codes(diags))
	}
}

// Single-package analysis does not check a qualified generic reference's arity.
func TestQualifiedGenericRefSinglePackageMode(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { p shared.Page<User> }
type User {}`))
	if findCode(diags, CodeGenericArity) != nil {
		t.Errorf("per-package mode should not validate qualified-ref arity, got %v", codes(diags))
	}
}

// A generic with the wrong arity in a map key is reported.
func TestGenericMapKeyArity(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type Page<T> { items T[] }
type User {}
type X { byPage map<Page<User, X>, string> }`))
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// walkTypeRefGenerics accepts a nil type ref.
func TestGenericWalkNilTypeRef(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.walkTypeRefGenerics(nil, nil)
	if len(a.diags) != 0 {
		t.Errorf("nil ref should not diag, got %v", a.diags)
	}
}
