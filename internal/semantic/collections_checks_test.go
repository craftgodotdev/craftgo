package semantic

import (
	"strings"
	"testing"
)

// @uniqueItems on a map is rejected.
func TestUniqueItemsOnMapRejected(t *testing.T) {
	expectError(t, `type Req { m map<string, int> @uniqueItems }`, CodeDecoratorTypeMismatch)
}

// @uniqueItems rejects a cross-package element whose nested struct field holds a slice.
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

// @uniqueItems accepts a cross-package element whose nested struct is comparable.
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

// @uniqueItems refuses an element holding a pointer, which the dedupe map
// compares by address: equal elements would pass as distinct.
func TestUniqueItemsRejectsPointerMembers(t *testing.T) {
	for _, c := range []struct{ label, src, member string }{
		{"optional primitive", "type O { val string? }", "O.val (string?)"},
		{"nullable primitive", "type O { val string @nullable }", "O.val (string)"},
		{"optional struct", "type Inner { id string }\ntype O { inner Inner? }", "O.inner (Inner?)"},
		{"optional enum", "enum S { A B }\ntype O { s S? }", "O.s (S?)"},
		{"file", "type O { upload file }", "O.upload (file)"},
		{"nested", "type Inner { id int? }\ntype O { inner Inner }", "O.inner.id (int?)"},
		{"generic argument", "type Pair<T> { val T? }\ntype O { p Pair<string> }", "O.p.val (string?)"},
	} {
		t.Run(c.label, func(t *testing.T) {
			d := expectError(t, c.src+"\ntype R { xs O[] @uniqueItems }", CodeDecoratorTypeMismatch)
			expectMessage(t, d, c.member+" is a pointer")
		})
	}
	// A pointer element of a generic instance is judged with its argument.
	expectError(t, `type Pair<T> { val T? }
type R { xs Pair<string>[] @uniqueItems }`, CodeDecoratorTypeMismatch)
}

// @uniqueItems refuses an element holding a value Go cannot compare, even
// behind `?`: bytes and any hold nil themselves, so `?` adds no pointer.
func TestUniqueItemsRejectsIncomparableMembers(t *testing.T) {
	for _, src := range []string{
		"type O { val bytes? }\ntype R { xs O[] @uniqueItems }",
		"type O { val any? }\ntype R { xs O[] @uniqueItems }",
		"type O { tags string[]? }\ntype R { xs O[] @uniqueItems }",
		"type Pair<T> { val T? }\ntype R { xs Pair<bytes>[] @uniqueItems }",
		"scalar Blob bytes\ntype O { b Blob }\ntype R { xs O[] @uniqueItems }",
		"type R { xs any[] @uniqueItems }",
	} {
		d := expectError(t, src, CodeDecoratorTypeMismatch)
		expectMessage(t, d, "is not comparable")
	}
}

// @uniqueItems accepts an element compared member by member.
func TestUniqueItemsAcceptsValueMembers(t *testing.T) {
	mustClean(t, `enum S { A B }
scalar Email string
type Inner { id string  n int }
type O { s S  e Email  inner Inner }
type R { xs O[] @uniqueItems  ys Email[] @uniqueItems  zs S[] @uniqueItems }`)
}

// @uniqueItems refuses a datetime element or member: its time.Time carries a
// location, so the dedupe map keeps two equal instants in different zones apart.
func TestUniqueItemsRejectsDatetime(t *testing.T) {
	for _, c := range []struct{ src, subject string }{
		{"type R { xs datetime[] @uniqueItems }", "datetime is a datetime"},
		{"type O { at datetime }\ntype R { xs O[] @uniqueItems }", "O.at (datetime) is a datetime"},
	} {
		d := expectError(t, c.src, CodeDecoratorTypeMismatch)
		expectMessage(t, d, c.subject)
	}
}

// A cross-package generic instance is judged with the arguments its referrer gives it.
func TestUniqueItemsCrossPkgGenericLocalArgument(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
type Box<T> { value T }`,
		"api.craftgo": `package design
import "lib"
type Item { tags string[] }
type R { xs lib.Box<Item>[] @uniqueItems }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeDecoratorTypeMismatch)
	if d == nil || !strings.Contains(d.Msg, "lib.Box<Item>.value.tags (string[]) is not comparable") {
		t.Fatalf("expected the local argument's slice reported, got %v", diags)
	}
}

// A map key naming no declared type gets the reference error alone.
func TestMapKeyUnknownNameLeftToReferenceCheck(t *testing.T) {
	expectNoCode(t, `type R { m map<Nope, int> }`, CodeMapKeyType)
}

// @uniqueItems rejects a cross-package generic instance over a non-comparable type argument.
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

// @uniqueItems accepts a cross-package generic instance over a comparable type argument.
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

// A non-marshalable map key inside a generic type argument is rejected, locally and across packages.
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

// A valid map key inside a generic type argument is accepted.
func TestMapKeyInGenericArgValidClean(t *testing.T) {
	mustClean(t, `type Box<T> { val T }
type Uses { b Box<map<string, int>> }`)
}

// An optional map key is rejected for every key kind, locally and across packages.
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

// @uniqueItems rejects a generic instance made non-comparable through a generic mixin.
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

// @uniqueItems accepts the generic-mixin shape over a comparable type argument.
func TestUniqueItemsGenericMixinComparableClean(t *testing.T) {
	mustClean(t, `type Inner<T> { val T }
type Box<T> { Inner<T> }
type Uses { rows Box<string>[] @uniqueItems }`)
}

// Two instances of one generic in a struct are judged separately, in either field order.
func TestUniqueItemsDistinctGenericInstancesRejected(t *testing.T) {
	expectError(t, `type Wrap<T> { v T }
type Holder { s Wrap<string>  b Wrap<bytes> }
type R { items Holder[] @uniqueItems }`, CodeDecoratorTypeMismatch)
	expectError(t, `type Wrap<T> { v T }
type Holder { b Wrap<bytes>  s Wrap<string> }
type R { items Holder[] @uniqueItems }`, CodeDecoratorTypeMismatch)

	// Likewise for a cross-package element.
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

// @uniqueItems accepts a struct holding two comparable instances of one generic.
func TestUniqueItemsDistinctGenericInstancesComparableClean(t *testing.T) {
	mustClean(t, `type Wrap<T> { v T }
type Holder { s Wrap<string>  b Wrap<int> }
type R { items Holder[] @uniqueItems }`)
}

// @uniqueItems rejects, once, a local element with a non-comparable cross-package generic field.
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

// @uniqueItems accepts a local element whose cross-package generic fields are comparable.
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
