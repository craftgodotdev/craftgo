package semantic

import "testing"

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

// `@lt(0)` on an unsigned field demands "value < 0", which no uint* can
// satisfy - the desugared spelling of `@negative`, which is already
// rejected. The capacity guard misses it (0 is itself in range).
func TestUnsignedLtZeroRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type T { c uint16 @lt(0) }`))
	if findCode(diags, CodeDecoratorTypeMismatch) == nil {
		t.Fatalf("expected @lt(0)-on-unsigned rejection; got %v", codes(diags))
	}
}

// `@lt(N)` with N>0 on unsigned is satisfiable (0..N-1) and must NOT be
// rejected - the guard targets only the empty predicate.
func TestUnsignedLtPositiveClean(t *testing.T) {
	mustClean(t, `type T { c uint16 @lt(10) }`)
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

// A contradictory bound on a CROSS-PACKAGE unsigned scalar must be caught
// (the per-package pass can't resolve the foreign scalar's primitive).
func TestCrossPkgUnsignedBoundRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Count uint32`,
		"api.craftgo": `package design
import "shared"
type T1 { n shared.Count @lt(0) }
type T2 { m shared.Count @lte(-1) }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeDecoratorTypeMismatch) || !hasCode(diags, CodeBoundOverflow) {
		t.Fatalf("expected unsigned @lt(0) + capacity-overflow rejections; got %v", codes(diags))
	}
}

// A cross-field group (@requiresOneOf) may reference a field promoted by a
// CROSS-PACKAGE mixin - the per-package pass can't expand it, but codegen
// resolves it via the project resolver, so it must not false-reject.
func TestCrossFieldOverCrossPkgMixinClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Contactable { email string?  phone string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, phone)
type Contact { shared.Contactable  note string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Errorf("cross-field over cross-pkg mixin must not false-reject; got: %s", d.Msg)
	}
}

// A cross-field group naming a member NO field provides must be rejected
// even when the type embeds a cross-package mixin. The per-package pass
// can't expand the foreign mixin so it defers; the project pass resolves
// the full field set and catches the typo. Without the project re-check
// the typo reaches codegen, which substitutes a literal `false` - a
// validator that silently never fires.
func TestCrossFieldTypoOverCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Contactable { email string?  phone string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, zzz)
type Contact { shared.Contactable  note string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("expected typo rejection over cross-pkg mixin; got %v", codes(diags))
	}
}

// The project re-check resolves NESTED cross-package mixins too: a member
// promoted two mixin levels deep is a real field (no false-reject), while
// a typo alongside it is still caught.
func TestCrossFieldTypoOverNestedCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Inner { email string?  phone string? }
type Outer { Inner  label string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, nope)
type Contact { shared.Outer }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("expected typo rejection over nested cross-pkg mixin; got %v", codes(diags))
	}
}

// A deeply-promoted member (two cross-package mixin levels) is a genuine
// field and must NOT be false-rejected - the control for the typo test.
func TestCrossFieldOverNestedCrossPkgMixinClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Inner { email string?  phone string? }
type Outer { Inner  label string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, phone)
type Contact { shared.Outer }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Errorf("nested cross-pkg promoted member must not false-reject; got: %s", d.Msg)
	}
}

// The project re-check must re-apply the per-field quality rules to a
// member promoted from a cross-package mixin - not only check the name
// exists. A PLAIN (non-optional) promoted member has no clean present/
// absent state, so a cross-field group referencing it is rejected exactly
// as a local plain member is. (Per-package can't see the foreign field, so
// without the re-check the rule was silently skipped.)
func TestCrossFieldPlainMemberOverCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/b.craftgo": `package base
type BaseMix { gamma string }`,
		"api.craftgo": `package design
import "base"
@requiresOneOf(alpha, gamma)
type Host { base.BaseMix  alpha string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) == nil {
		t.Fatalf("expected plain cross-pkg-promoted member rejection; got %v", codes(diags))
	}
}

// A @default member promoted from a cross-package mixin is rejected too
// (a defaulted field is always present, making the group a no-op the
// OpenAPI contradicts) - the same rule the local case enforces.
func TestCrossFieldDefaultMemberOverCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/b.craftgo": `package base
type BaseMix { gamma string? @default("x") }`,
		"api.craftgo": `package design
import "base"
@requiresOneOf(alpha, gamma)
type Host { base.BaseMix  alpha string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) == nil {
		t.Fatalf("expected @default cross-pkg-promoted member rejection; got %v", codes(diags))
	}
}

// A clean optional member promoted from a cross-package mixin alongside a
// clean local one must NOT double-report or false-reject - the control.
func TestCrossFieldCleanMembersOverCrossPkgMixinClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/b.craftgo": `package base
type BaseMix { gamma string? }`,
		"api.craftgo": `package design
import "base"
@requiresOneOf(alpha, gamma)
type Host { base.BaseMix  alpha string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeCrossFieldNotOptional); d != nil {
		t.Errorf("clean cross-pkg-promoted member must not be rejected; got: %s", d.Msg)
	}
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Errorf("clean members must not false-reject; got: %s", d.Msg)
	}
}

// An auto-promoted @query field (no binding decorator, body-less verb) of a
// multi-dimensional array type must be rejected just like the explicit
// `int[][] @query` form - the depth guard lives in the shared
// isWireBindingType predicate so the auto-@query path catches it too.
func TestAutoQueryMultiDimArrayRejected(t *testing.T) {
	expectError(t, `type Req { grid int[][] }
service S { get Op /x { request Req } }`, CodeBindingType)
}

// A 1-D auto-@query array is fine - only nested arrays are rejected.
func TestAutoQuerySingleDimArrayClean(t *testing.T) {
	mustClean(t, `type Req { tags string[] }
service S { get Op /x { request Req } }`)
}

// An integer @default outside the field primitive's capacity (negative on
// unsigned, or out of a narrow int's range) would emit a non-compiling
// cast (`uint(-5)` / `int8(200)`); rejected at design time.
func TestDefaultOutOfRangeRejected(t *testing.T) {
	expectError(t, `type Req { u uint? @default(-5) }`, CodeBoundOverflow)
	expectError(t, `type Req { b int8? @default(200) }`, CodeBoundOverflow)
}

// An in-range @default on a narrow int is accepted.
func TestDefaultInRangeClean(t *testing.T) {
	mustClean(t, `type Req { b int8? @default(100)  u uint8? @default(0) }`)
}

// @default on a `bytes` field has no unambiguous literal form (Go []byte vs
// OpenAPI base64); rejected rather than emitting non-compiling Go.
func TestBytesDefaultRejected(t *testing.T) {
	expectError(t, `type Req { p bytes? @default("Ynl0ZXM=") }`, CodeDecoratorConflict)
	expectError(t, `type Req { ps bytes[]? @default(["YQ=="]) }`, CodeDecoratorConflict)
}

// A multi-dimensional array @default is rejected: a default may target a
// primitive / scalar / enum or a single-level array of those, not a nested
// array. Covers primitive and named (enum) element types.
func TestMultiDimArrayDefaultRejected(t *testing.T) {
	expectError(t, `type Req { grid int[][]? @default([[1, 2], [3, 4]]) }`, CodeDecoratorConflict)
	expectError(t, `enum Color { Red  Green  Blue }
type Req { swatch Color[][]? @default([[Red, Green], [Blue]]) }`, CodeDecoratorConflict)
}

// A single-level array @default is still accepted (the established shape).
func TestSingleDimArrayDefaultClean(t *testing.T) {
	mustClean(t, `type Req { arr int[]? @default([1, 2, 3]) }`)
}

// A multi-dimensional array @example is rejected with the same structural
// message @default uses, rather than the per-element walk misreporting the
// inner array as "expects a single value" (the @default/@example parity twin).
func TestMultiDimArrayExampleRejected(t *testing.T) {
	expectError(t, `type Req { rows int[][] @example([[1, 2], [3, 4]]) }`, CodeDecoratorConflict)
}

// A single-level array @example is still accepted.
func TestSingleDimArrayExampleClean(t *testing.T) {
	mustClean(t, `type Req { flat int[] @example([1, 2, 3]) }`)
}

// @uniqueItems applies to arrays, not maps: a map collapses to PrimArray in the
// applicability gate but neither codegen stage honours it, so reject rather
// than silently drop the constraint (matching the int rejection).
func TestUniqueItemsOnMapRejected(t *testing.T) {
	expectError(t, `type Req { m map<string, int> @uniqueItems }`, CodeDecoratorTypeMismatch)
}

// `file` is a multipart-upload wire keyword, not a Go type, so a scalar may
// not wrap it (`scalar X file` would emit non-compiling `type X file`) - reject
// it like `any`, which is already rejected.
func TestScalarOverFileRejected(t *testing.T) {
	expectError(t, `scalar FileScalar file
type R { f FileScalar }`, CodeScalarBadPrimitive)
}

// The file validators @maxSize / @mimeTypes are pointless on a @sensitive
// field (it never crosses the wire), so they conflict - like every other
// validator already listed in sensitiveConflicts.
func TestSensitiveConflictsFileValidators(t *testing.T) {
	expectError(t, `type Req { secret file @sensitive @maxSize(1000) }`, CodeDecoratorConflict)
	expectError(t, `type Req { secret file @sensitive @mimeTypes(["image/png"]) }`, CodeDecoratorConflict)
}

// An empty `@path("")` wire-name arg falls back to the field name rather
// than false-rejecting the path-param check.
func TestEmptyPathWireNameClean(t *testing.T) {
	mustClean(t, `type Req { foo string @path("") }
service S { get G /users/{foo} { request Req } }`)
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

// TestDeclNamedAfterBuiltinRejected: a type/enum/scalar/error named after a
// built-in spelling shadows the built-in in generated Go and won't compile, so
// it is rejected. Middleware names live in a separate Go namespace (exempt).
func TestDeclNamedAfterBuiltinRejected(t *testing.T) {
	expectError(t, `scalar int string`, CodeDeclBuiltinName)
	expectError(t, `type string { a int }`, CodeDeclBuiltinName)
	expectError(t, `enum bool { X Y }`, CodeDeclBuiltinName)
	expectError(t, `error NotFound any`, CodeDeclBuiltinName)
	// Middleware lives in a separate Go namespace, so a builtin name is NOT a
	// collision error (it may still warn about the lowercase name).
	if _, diags := AnalyzeWith(parseFiles(t, `middleware int`), Options{}); findCode(diags, CodeDeclBuiltinName) != nil {
		t.Error("middleware named after a builtin should not be a builtin-collision error")
	}
	mustClean(t, `scalar Email string  scalar UserID string`)
}

// TestObjectFieldTypeRejected: `object` is a broken half-alias (its Go renderer
// emits an undefined type + dangling $ref); reject it and point at `any`.
func TestObjectFieldTypeRejected(t *testing.T) {
	expectError(t, `type X { f object }`, CodeRefUnknownSymbol)
	expectError(t, `type X { f object[] }`, CodeRefUnknownSymbol)
	// `any` is the valid arbitrary type.
	mustClean(t, `type X { f any  g any[] }`)
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

// A cross-package scalar / enum field carrying `@nullable` that auto-binds to
// @query on a body-less verb (GET/DELETE) must be rejected - `@nullable` lowers
// it to a pointer but the wire binder writes a non-pointer value into it
// (`req.Nul = lib.Email(...)` into a `*lib.Email`), non-compiling. The local
// equivalent is already rejected; this mirrors it for the qualified form (the
// check is structural, so it runs before the qualified-ref deferral).
func TestNullableCrossPkgAutoQueryRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
scalar Email string @format(email)
enum Color { Red  Green  Blue }`,
		"api.craftgo": `package design
import "lib"
type XReq { nul lib.Email @nullable }
type XResp { ok bool }
service XSvc { get X /x { request XReq  response XResp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorConflict) == nil {
		t.Fatalf("expected cross-pkg @nullable auto-@query rejection; got %v", codes(diags))
	}
}

// A cross-package binding error must be reported EXACTLY ONCE. The project
// binding pass used to walk every request body a second time (request types
// are already in pkg.Types), emitting byte-identical duplicate diagnostics.
func TestCrossPkgBindingDiagnosticNotDuplicated(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Blob bytes`,
		"api.craftgo": `package design
import "shared"
type SearchReq { q shared.Blob @query }
service S { post Do /do { request SearchReq } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	n := 0
	for i := range diags {
		if diags[i].Code == CodeBindingType {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 binding diagnostic, got %d: %v", n, codes(diags))
	}
}

// An auto-@path field (name matches a {segment}, no binding decorator) must
// reject @nullable / optional `?` / @default on any verb - a matched route
// always supplies the segment and the path binder writes a plain string, so
// these either non-compile (pointer mismatch) or are meaningless.
func TestAutoPathFieldDecoratorsRejected(t *testing.T) {
	expectError(t, `type R { id string @nullable }
service S { get G /it/{id} { request R } }`, CodeDecoratorConflict)
	expectError(t, `type R { id string?  name string }
service S { post P /it/{id} { request R } }`, CodeDecoratorConflict)
	expectError(t, `type R { id string @default("x") }
service S { get G /it/{id} { request R } }`, CodeDecoratorConflict)
}

// A normal auto-path string field is fine - the control.
func TestAutoPathPlainStringClean(t *testing.T) {
	mustClean(t, `type R { id string }
service S { get G /it/{id} { request R } }`)
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

// A `file` field nested below the top level of a request body cannot be bound:
// the multipart binder reads only the resolved top-level request fields, so the
// `*multipart.FileHeader` stays nil and the upload is silently lost while gen
// and `go build` both succeed. Reject the nesting. A top-level `file` (direct
// or flattened in via a mixin) and a `file` carried in a response (including a
// type echoed back as its own response) stay valid.
func TestNestedRequestFileRejected(t *testing.T) {
	expectError(t, `package design
type Wrap { data file @form }
type UploadReq { wrapper Wrap }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	// Reached two levels down.
	expectError(t, `package design
type Leaf { data file }
type Mid { leaf Leaf }
type UploadReq { mid Mid }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	mustNoFilePosition := func(label, src string) {
		t.Helper()
		_, diags := Analyze(parseFiles(t, src))
		if d := findCode(diags, CodeFilePosition); d != nil {
			t.Errorf("%s: unexpected file-position rejection: %s", label, d.Msg)
		}
	}
	// Top-level file binds directly.
	mustNoFilePosition("top-level", `package design
type UploadReq { f file @form  name string }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`)
	// A mixin flattens its file into the host's top level.
	mustNoFilePosition("mixin", `package design
type Bits { f file @form }
type UploadReq { Bits  name string }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`)
	// A file echoed back in a response is an established modelling pattern.
	mustNoFilePosition("echo", `package design
type Profile { avatar file @form  name string }
service S { post Up /up { request Profile  response Profile } }`)
}

// A 1-D `file[]` binds every repeated multipart part into a
// []*multipart.FileHeader; a multi-dimensional `file[][]` has no multipart
// encoding and is rejected at gen time rather than emitting non-compiling Go.
func TestMultiDimFileArrayRejected(t *testing.T) {
	expectError(t, `package design
type UploadReq { grid file[][]  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	// 1-D file[] is accepted - auto-form and explicit @form alike.
	for label, src := range map[string]string{
		"auto": `package design
type UploadReq { files file[]  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`,
		"explicit": `package design
type UploadReq { files file[] @form  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`,
	} {
		if _, diags := Analyze(parseFiles(t, src)); findCode(diags, CodeFilePosition) != nil {
			t.Errorf("%s file[]: unexpected file-position rejection", label)
		}
	}
}
