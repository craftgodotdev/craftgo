package semantic

import "testing"

// @uniqueItems applies to arrays, not maps: a map collapses to PrimArray in the
// applicability gate but neither codegen stage honours it, so reject rather
// than silently drop the constraint (matching the int rejection).
func TestUniqueItemsOnMapRejected(t *testing.T) {
	expectError(t, `type Req { m map<string, int> @uniqueItems }`, CodeDecoratorTypeMismatch)
}

// @uniqueItems over a cross-package struct element that is only TRANSITIVELY
// non-comparable - through a bare member of the foreign struct that itself
// holds a slice - must be rejected. The comparability walk has to follow the
// foreign struct's bare member into ITS home package; without threading that
// package the member resolved to "unknown" and was conservatively accepted,
// shipping a non-compiling `map[dep.XOuter]struct{}` dedup.
func TestUniqueItemsCrossPkgTransitiveNonComparableRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"dep/d.craftgo": `package dep
type XInner { tags string[] }
type XOuter { id string  inner XInner }`,
		"api.craftgo": `package design
import "dep"
type NReq { items dep.XOuter[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected transitive non-comparable cross-pkg @uniqueItems rejection; got %v", codes(diags))
	}
}

// The same shape but with the foreign nested member comparable (a plain
// string, not a slice) must NOT be rejected - the control.
func TestUniqueItemsCrossPkgTransitiveComparableClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"dep/d.craftgo": `package dep
type XInner { tags string }
type XOuter { id string  inner XInner }`,
		"api.craftgo": `package design
import "dep"
type NReq { items dep.XOuter[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorTypeMismatch); d != nil {
		t.Errorf("comparable cross-pkg element must not be rejected; got: %s", d.Msg)
	}
}

// @uniqueItems on an array of a struct with an OPTIONAL field whose underlying
// type is non-comparable (a slice inside it) must NOT be rejected: `?` makes
// the field a Go pointer (`*T`), which is comparable, so the struct is a valid
// map key. A non-optional such field stays correctly rejected.
func TestUniqueItemsOptionalFieldComparable(t *testing.T) {
	mustClean(t, `type Inner { id string  tags string[] }
type Holder { inner Inner? }
type R { xs Holder[] @uniqueItems }`)
	// The non-optional twin is still rejected (Inner embedded by value).
	expectError(t, `type Inner { id string  tags string[] }
type Holder { inner Inner }
type R { xs Holder[] @uniqueItems }`, CodeDecoratorTypeMismatch)
	// Cross-package optional field is likewise a comparable pointer.
	root, files := projectFixture(t, map[string]string{
		"dep/d.craftgo": `package dep
type XInner { id string  tags string[] }`,
		"api.craftgo": `package design
type Holder { inner dep.XInner? }
type NReq { items Holder[] @uniqueItems }`,
	})
	if _, diags := AnalyzeProject(files, Options{DesignRoot: root}); findCode(diags, CodeDecoratorTypeMismatch) != nil {
		t.Error("optional cross-pkg struct field is a comparable pointer; must not be rejected")
	}
}

// @uniqueItems over a cross-package GENERIC instance whose type-arg makes it
// non-comparable (`shared.Box<shared.User>` where User holds a slice) must be
// rejected. The comparability walk has to substitute the type-args into the
// generic decl's fields - mirroring the same-package twin - or the bare `T`
// resolves to nothing, the instance is conservatively accepted, and codegen
// emits a non-compiling `map[shared.Box[...]]struct{}`.
func TestUniqueItemsCrossPkgGenericNonComparableRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type User { id string  roles string[] }
type Box<T> { value T }`,
		"api.craftgo": `package design
import "shared"
type UniqueHost { rows shared.Box<shared.User>[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected cross-pkg generic-instance non-comparable @uniqueItems rejection; got %v", codes(diags))
	}
}

// A cross-package generic instance with a COMPARABLE type-arg
// (`shared.Box<string>`) must NOT be rejected - the control.
func TestUniqueItemsCrossPkgGenericComparableClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Box<T> { value T }`,
		"api.craftgo": `package design
import "shared"
type UniqueHost { rows shared.Box<string>[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorTypeMismatch); d != nil {
		t.Errorf("comparable cross-pkg generic instance must not be rejected; got: %s", d.Msg)
	}
}

// A non-marshalable map KEY nested inside a generic type-argument
// (`Box<map<WithSlice, string>>`) must be rejected - a struct/slice key is a
// non-compiling Go map key, a bool/float/bytes key panics at json.Marshal.
// The comparability walk has to descend into the generic's type-args, not
// only the field's top-level map/array. Covers single-package (struct key)
// and cross-package (float-scalar key) forms.
func TestMapKeyInGenericArgRejected(t *testing.T) {
	expectError(t, `type Box<T> { val T }
type WithSlice { tags string[] }
type Uses { b Box<map<WithSlice, string>> }`, CodeMapKeyType)

	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
scalar FloatKey float64
type Box<T> { val T }`,
		"api.craftgo": `package design
import "lib"
type UsesF { b lib.Box<map<lib.FloatKey, string>> }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMapKeyType) == nil {
		t.Fatalf("expected cross-pkg map-key-in-generic-arg rejection; got %v", codes(diags))
	}
}

// A VALID map key inside a generic type-arg (`Box<map<string, int>>`) must
// NOT be rejected - the control.
func TestMapKeyInGenericArgValidClean(t *testing.T) {
	mustClean(t, `type Box<T> { val T }
type Uses { b Box<map<string, int>> }`)
}

// TestOptionalMapKeyRejected: an optional `?` map key renders map[*K]V, which
// encoding/json cannot marshal (pointer object keys fail) - so it is rejected
// for every underlying key kind (primitive, enum, scalar), local and
// cross-package. A re-added non-optional key is the control.
func TestOptionalMapKeyRejected(t *testing.T) {
	for _, key := range []string{"string?", "int?", "Color?", "Code?"} {
		expectError(t, "enum Color { Red Green }\nscalar Code string\ntype T { m map<"+key+", int> }", CodeMapKeyType)
	}
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": "package lib\nscalar ID string",
		"api.craftgo":   "package design\ntype T { m map<lib.ID?, int> }",
	})
	if _, diags := AnalyzeProject(files, Options{DesignRoot: root}); findCode(diags, CodeMapKeyType) == nil {
		t.Fatalf("expected cross-pkg optional map-key rejection; got %v", codes(diags))
	}
	mustClean(t, "enum Color { Red Green }\ntype T { m map<string, int>  n map<Color, int> }")
}

// @uniqueItems over a generic instance whose non-comparability arrives via a
// GENERIC MIXIN of the type-param (`Box<bytes>` where `Box<T>` embeds
// `Inner<T>` and `Inner{ val T }`) must be rejected. The comparability walk
// has to substitute the outer type-args into the mixin ref before descending,
// or the bare `T` inside the mixin escapes and a non-compiling dedupe map
// (`map[Box[[]byte]]struct{}`) is emitted. Covers single + cross-package.
func TestUniqueItemsGenericMixinNonComparableRejected(t *testing.T) {
	expectError(t, `type Inner<T> { val T }
type Box<T> { Inner<T> }
type Uses { rows Box<bytes>[] @uniqueItems }`, CodeDecoratorTypeMismatch)

	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Inner<T> { val T }
type Box<T> { Inner<T> }
type User { id string  roles string[] }`,
		"api.craftgo": `package design
import "shared"
type Uses { rows shared.Box<shared.User>[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected generic-mixin non-comparable cross-pkg @uniqueItems rejection; got %v", codes(diags))
	}
}

// The same shape with a comparable type-arg (`Box<string>`) must NOT be
// rejected - the control.
func TestUniqueItemsGenericMixinComparableClean(t *testing.T) {
	mustClean(t, `type Inner<T> { val T }
type Box<T> { Inner<T> }
type Uses { rows Box<string>[] @uniqueItems }`)
}

// Two DIFFERENT instantiations of one generic in the same struct
// (`Wrap<string>` comparable, `Wrap<bytes>` not) must be judged
// independently: the comparability back-edge guard is keyed by the
// instantiated identity, not the bare decl name, so the comparable instance
// can't poison the guard for the non-comparable one (which would leak a
// non-compiling `map[Holder]struct{}`). Order-independent: covers both.
func TestUniqueItemsDistinctGenericInstancesRejected(t *testing.T) {
	expectError(t, `type Wrap<T> { v T }
type Holder { s Wrap<string>  b Wrap<bytes> }
type R { items Holder[] @uniqueItems }`, CodeDecoratorTypeMismatch)
	// reversed order - the non-comparable instance comes first
	expectError(t, `type Wrap<T> { v T }
type Holder { b Wrap<bytes>  s Wrap<string> }
type R { items Holder[] @uniqueItems }`, CodeDecoratorTypeMismatch)

	// Cross-package: the element itself is qualified (`lib.Holder`) so the
	// project comparability pass resolves it; its two `Wrap` instantiations
	// must stay distinct in the back-edge guard.
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
type Wrap<T> { v T }
type Holder { s Wrap<string>  b Wrap<bytes> }`,
		"api.craftgo": `package design
import "lib"
type R { items lib.Holder[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected cross-pkg distinct-generic-instance @uniqueItems rejection; got %v", codes(diags))
	}
}

// Two comparable instantiations (`Wrap<string>`, `Wrap<int>`) must NOT be
// rejected - the control proving the per-instantiation key doesn't over-reject.
func TestUniqueItemsDistinctGenericInstancesComparableClean(t *testing.T) {
	mustClean(t, `type Wrap<T> { v T }
type Holder { s Wrap<string>  b Wrap<int> }
type R { items Holder[] @uniqueItems }`)
}

// @uniqueItems over a LOCAL element whose field reaches a cross-package
// non-comparable generic (`Holder{ b lib.Wrap<bytes> }`, `Holder[]`) must be
// rejected - neither pass owned this combination before. It must fire exactly
// once (no double-report with the per-package pass), and a comparable variant
// and a fully-local non-comparable element must each behave correctly.
func TestUniqueItemsLocalElementCrossPkgFieldRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
type Wrap<T> { v T }`,
		"api.craftgo": `package design
import "lib"
type Holder { s lib.Wrap<string>  b lib.Wrap<bytes> }
type R { items Holder[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	n := 0
	for i := range diags {
		if diags[i].Code == CodeDecoratorTypeMismatch {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 @uniqueItems rejection, got %d: %v", n, codes(diags))
	}
}

// The comparable variant (cross-pkg arg is comparable) must NOT be rejected.
func TestUniqueItemsLocalElementCrossPkgFieldComparableClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
type Wrap<T> { v T }`,
		"api.craftgo": `package design
import "lib"
type Holder { s lib.Wrap<string>  b lib.Wrap<int> }
type R { items Holder[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorTypeMismatch); d != nil {
		t.Errorf("comparable cross-pkg field must not be rejected; got: %s", d.Msg)
	}
}
