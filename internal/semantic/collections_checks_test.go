package semantic

import "testing"

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

// An optional struct field is a comparable pointer, so @uniqueItems accepts its holder.
func TestUniqueItemsOptionalFieldComparable(t *testing.T) {
	mustClean(t, `type Inner { id string  tags string[] }
type Holder { inner Inner? }
type R { xs Holder[] @uniqueItems }`)
	// A non-optional field holds Inner by value.
	expectError(t, `type Inner { id string  tags string[] }
type Holder { inner Inner }
type R { xs Holder[] @uniqueItems }`, CodeDecoratorTypeMismatch)
	// An optional cross-package struct field is a pointer too.
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
