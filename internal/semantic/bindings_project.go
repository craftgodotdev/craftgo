package semantic

// Project-level binding-type check. Mirror of
// [analyzer.checkBindingFieldType] but uses the full
// [Project.Packages] map so qualified refs like `shared.Email @path`
// resolve through to the foreign-package scalar / enum, instead of
// false-rejecting at the per-package layer where only the local
// pkg.Scalars / pkg.Enums maps are in scope.
//
// The per-package analyzer sets [Options.skipBindingTypeCheckQualified]
// in project mode so qualified-ref binding fields skip the local
// diagnostic; this pass re-runs the same shape rules against the
// project-wide view and emits [CodeBindingType] when a cross-package
// binding turns out to violate one.

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

func (r *refResolver) checkProjectBindings() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		// Every request type is a declared type already in pkg.Types, so
		// iterating the type set once covers them too — a second pass over
		// service request types would re-emit byte-identical diagnostics.
		for _, td := range pkg.Types {
			r.checkBindingsInBody(td.Name, td.Body)
		}
		// Error bodies carry @header / @cookie response bindings too, so a
		// qualified cross-package field on one must satisfy the same wire-type
		// rule — the per-package pass defers qualified refs to here. Mirrors
		// checkProjectFieldRules, which already iterates both sets.
		for _, ed := range pkg.Errors {
			r.checkBindingsInBody(ed.Name, ed.Body)
		}
	}
}

func (r *refResolver) checkBindingsInBody(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		if !isQualifiedTypeRef(f.Type) {
			continue
		}
		r.checkBindingsOnQualifiedField(parent, f)
	}
}

func (r *refResolver) checkBindingsOnQualifiedField(parent string, f *ast.Field) {
	for _, d := range f.Decorators {
		switch d.Name {
		case wire.BindingPath:
			if r.qualifiedIsPathBindable(f.Type) {
				continue
			}
			r.diagBinding(d, "field %s.%s: @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
		case wire.BindingQuery, wire.BindingHeader, wire.BindingCookie:
			if d.Name == wire.BindingCookie && f.Type.Array {
				r.diagBinding(d, "field %s.%s: @cookie cannot bind to an array - cookies carry a single value per name",
					parent, f.Name)
				continue
			}
			if r.qualifiedIsWireBindable(f.Type) {
				continue
			}
			r.diagBinding(d, "field %s.%s: @%s requires string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or generic instantiations) - got %s",
				parent, f.Name, d.Name, describeTypeRef(f.Type))
		case wire.BindingForm:
			if r.qualifiedIsFormBindable(f.Type) {
				continue
			}
			r.diagBinding(d, "field %s.%s: @form requires `file` or string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or file arrays) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
		}
	}
}

// qualifiedIsPathBindable is the cross-package twin of
// [isPathBindingType]: a `@path` field is wire-bindable but never
// optional (a matched route always supplies the segment) nor an array
// (a path carries a single value per segment). The scalar / enum lookup
// walks the project's package map for `pkg.Name` refs.
func (r *refResolver) qualifiedIsPathBindable(t *ast.TypeRef) bool {
	if t == nil || t.Optional || t.Array {
		return false
	}
	return r.qualifiedIsWireBindable(t)
}

func (r *refResolver) qualifiedIsWireBindable(t *ast.TypeRef) bool {
	if t == nil || t.Map != nil || t.Named == nil || len(t.Named.Args) > 0 {
		return false
	}
	// Nested arrays have no wire-string encoding (see [isWireBindingType]);
	// reject the cross-package twin identically so a qualified element type
	// (`shared.Tag[][]`) auto-binding to @query is caught too.
	if t.ArrayDepth > 1 {
		return false
	}
	if sc := r.lookupScalar(t.Named); sc != nil {
		return isPrimitiveWireName(sc.Primitive)
	}
	if ed := r.lookupEnum(t.Named); ed != nil {
		return enumWireKindOK(ed)
	}
	return false
}

func (r *refResolver) qualifiedIsFormBindable(t *ast.TypeRef) bool {
	// `file` is never qualified — bare primitive only — so the form
	// check on a qualified ref collapses to the wire rules.
	return r.qualifiedIsWireBindable(t)
}

// enumWireKindOK returns true when the enum's first member is one of
// the wire-bindable kinds (bare / string / int).
func enumWireKindOK(ed *ast.EnumDecl) bool {
	for _, m := range ed.Members {
		if v, ok := m.(*ast.EnumValue); ok {
			switch v.Kind {
			case ast.EnumBare, ast.EnumString, ast.EnumInt:
				return true
			}
		}
	}
	return false
}

// checkProjectFieldRules is the project-level twin of every per-package
// field guard that resolves a field's primitive / category through a
// LOCAL table — `a.pkg.Scalars` / `a.pkg.Enums` / `a.pkg.Types` /
// `fieldPrim` — and therefore silently no-ops on a QUALIFIED
// cross-package ref (`shared.X` misses the bare-keyed local map). It
// resolves the referenced scalar / enum / type ONCE via the project
// resolver and re-runs the decorator-category (AppliesTo), @multipleOf
// target, numeric-bound, @uniqueItems comparability, and map-key
// marshalability checks. A single home for the cross-package field rules:
// a new guard adds one branch here, not a whole new pass. Every check
// fires only on the QUALIFIED form, so none double-reports with the
// per-package pass (which owns bare / local refs).
func (r *refResolver) checkProjectFieldRules() {
	for pkgName, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, td := range pkg.Types {
			r.checkFieldRules(pkgName, td.Name, td.Body)
		}
		for _, ed := range pkg.Errors {
			r.checkFieldRules(pkgName, ed.Name, ed.Body)
		}
	}
}

func (r *refResolver) checkFieldRules(currentPkg, parent string, body []ast.TypeMember) {
	for _, m := range body {
		f, ok := m.(*ast.Field)
		if !ok || f.Type == nil {
			continue
		}
		// A field whose own type is a qualified scalar: decorator category,
		// @multipleOf target, and numeric-bound contradictions.
		if prim, ok := r.qualifiedScalarPrim(f.Type); ok {
			r.checkAppliesToProject(parent, f, PrimFromName(prim))
			r.checkMultipleOfFloatProject(f, prim)
			r.checkScalarBoundContradictions(f, prim)
		}
		// @uniqueItems over an array whose element isn't comparable through a
		// cross-package type (qualified element, or a local element reaching one).
		if f.Type.Array {
			r.checkUniqueItemsProject(currentPkg, f)
		}
		// A map with a qualified key that JSON can't marshal — at the field's
		// top level, inside an array, or nested in a generic type-argument
		// (`Box<map<bad, V>>`). checkMapKeysProject walks all three; it is a
		// no-op for a type that holds no map, so call it unconditionally.
		r.checkMapKeysProject(f.Type, f)
	}
}

// qualifiedScalarPrim returns the underlying primitive of a 2-part
// qualified scalar field type (`shared.Count` → `uint32`), or ok=false.
func (r *refResolver) qualifiedScalarPrim(t *ast.TypeRef) (string, bool) {
	if t == nil || t.Array || t.Map != nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 2 {
		return "", false
	}
	if sc := r.lookupScalar(t.Named); sc != nil {
		return sc.Primitive, true
	}
	return "", false
}

// checkAppliesToProject re-runs the decorator AppliesTo category check
// ([analyzer.checkBodyTypeCompat]) for a qualified-scalar field whose
// category the per-package fieldPrim returned as PrimAny.
func (r *refResolver) checkAppliesToProject(parent string, f *ast.Field, cat Prims) {
	if cat == 0 {
		return
	}
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		spec, ok := Lookup(d.Name)
		if !ok || spec.AppliesTo == 0 {
			continue
		}
		if spec.AppliesTo&cat == 0 {
			r.diag(d.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@%s applies to %s fields, but %s.%s is %s", d.Name, spec.AppliesTo, parent, f.Name, cat)
		}
	}
}

// checkMultipleOfFloatProject rejects `@multipleOf` on a qualified FLOAT
// scalar — Go's modulus is integer-only, so the validator silently drops
// it ([analyzer.checkMultipleOfTarget] misses the cross-package primitive).
func (r *refResolver) checkMultipleOfFloatProject(f *ast.Field, prim string) {
	if prim != "float32" && prim != "float64" {
		return
	}
	for _, d := range f.Decorators {
		if d != nil && d.Name == "multipleOf" {
			r.diag(d.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@multipleOf does not support float fields — Go's modulus operator is integer-only. Move the field to an integer type or add a tolerance check in your handler.")
		}
	}
}

// checkUniqueItemsProject rejects `@uniqueItems` on an array whose element is
// non-comparable due to a CROSS-PACKAGE type — either the element itself is a
// qualified non-comparable type (`shared.Blob[]` over `scalar Blob bytes`), or
// a LOCAL element transitively reaches one through a field (`Holder{ b
// lib.Wrap<bytes> }`). Both would emit a non-compiling `map[T]struct{}`. A
// fully-local non-comparable element is owned by the per-package pass; this
// pass fires only when a cross-package ref is the cause, so the two never
// double-report. `currentPkg` is the package the element's bare refs resolve
// against.
func (r *refResolver) checkUniqueItemsProject(currentPkg string, f *ast.Field) {
	elem := peelOneArray(f.Type)
	if elem == nil || elem.Named == nil || elem.Named.Name == nil {
		return
	}
	var nonComparable bool
	switch len(elem.Named.Name.Parts) {
	case 2:
		// Qualified element — owned entirely by this pass (per-package skips it).
		nonComparable = !r.projectComparable(elem, "", false, map[string]bool{})
	case 1:
		// Local element — reject only when resolving its cross-package fields
		// changes the verdict (the per-package pass, which can't see them,
		// passed it). A purely-local non-comparable element is left to that pass.
		localView := r.projectComparable(elem, currentPkg, true, map[string]bool{})
		fullView := r.projectComparable(elem, currentPkg, false, map[string]bool{})
		nonComparable = localView && !fullView
	default:
		return
	}
	if !nonComparable {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != "uniqueItems" {
			continue
		}
		r.diag(d.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
			"@uniqueItems requires comparable elements (usable as a map key) — %s is not (a slice / map / `any`, or a struct/generic containing one, possibly through a cross-package field). Restructure the element into a comparable shape, or drop @uniqueItems.",
			describeTypeRef(elem))
	}
}

// projectComparable is the cross-package twin of
// [analyzer.typeRefComparable]: a value usable as a Go map key. Arrays /
// maps / `any` / `bytes` are not; a qualified struct is comparable only
// when every member is. `currentPkg` is the package a BARE named ref
// resolves against — empty at the (qualified) top-level call, and set to a
// foreign struct's own package when recursing into its members, so a bare
// member of a foreign struct (`XInner` inside `dep.XOuter`) is followed into
// that package rather than conservatively accepted (which let a transitively
// non-comparable element through to a non-compiling `map[T]struct{}`).
// `conservative` mimics the per-package view: a QUALIFIED (cross-package) ref
// is treated as comparable without resolving it, exactly as the per-package
// pass does (it has no project view). Running once conservative and once full
// and rejecting only when the two DISAGREE isolates a non-comparability that
// arrives through a cross-package field of an otherwise-local element — which
// the per-package pass misses — without double-reporting a fully-local
// non-comparable element the per-package pass already rejects.
func (r *refResolver) projectComparable(t *ast.TypeRef, currentPkg string, conservative bool, seen map[string]bool) bool {
	if t == nil || t.Array || t.Map != nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	switch t.Named.Name.String() {
	case "any", "bytes", "file":
		return false
	case "string", "bool",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	// Resolve the named type's home package + symbol. A qualified ref names
	// its package; a bare ref takes its symbol directly and inherits
	// currentPkg (the struct it belongs to) — splitQualified yields no symbol
	// for a bare name, so read Parts here.
	parts := t.Named.Name.Parts
	var pkgName, sym string
	switch len(parts) {
	case 1:
		pkgName, sym = currentPkg, parts[0]
	case 2:
		if conservative {
			return true // cross-package ref — mimic the per-package view
		}
		pkgName, sym = parts[0], parts[1]
	default:
		return true
	}
	if pkgName == "" {
		return true // no resolution context — per-package pass owns it
	}
	pkg := r.proj.Packages[pkgName]
	if pkg == nil {
		return true
	}
	if sc, ok := pkg.Scalars[sym]; ok && sc != nil {
		return sc.Primitive != "bytes"
	}
	if _, ok := pkg.Enums[sym]; ok {
		return true
	}
	td, ok := pkg.Types[sym]
	if !ok {
		return true // unresolved — conservative
	}
	// Key the back-edge guard by the instantiated identity (name + args), not
	// the bare decl name, so different instantiations of one generic stay
	// distinct (see [typeRefComparable]); a comparable instance can't poison
	// the guard for a later non-comparable one.
	key := comparableKey(t)
	if seen[key] {
		return true
	}
	seen[key] = true
	// For a generic instance (`Box<shared.User>`) substitute the type-args
	// into the decl's fields so a field typed `T` is judged against the
	// concrete argument — mirroring the same-package twin typeRefComparable.
	// Without it a bare `T` resolves to nothing and falls through to
	// "conservatively comparable", letting a non-comparable instance pass and
	// then emit a non-compiling `map[Box[...]]struct{}`.
	subst := map[string]*ast.TypeRef{}
	if len(td.TypeParams) > 0 {
		for i, tp := range td.TypeParams {
			if i < len(t.Named.Args) {
				subst[tp] = t.Named.Args[i]
			}
		}
	}
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			ft := substTypeParam(v.Type, subst)
			// An optional `?` non-collection field renders as a comparable Go
			// pointer (`*T`) - mirror typeRefComparable and don't descend past it.
			if ft != nil && ft.Optional && !ft.Array && ft.Map == nil {
				continue
			}
			if !r.projectComparable(ft, pkgName, conservative, seen) {
				return false
			}
		case *ast.Mixin:
			if v.Ref != nil && v.Ref.Name != nil {
				// Substitute the outer decl's type-args into the mixin ref too
				// (`Inner<T>` → `Inner<shared.User>`), mirroring the Field
				// branch — without it a bare `T` inside a generic mixin escapes.
				if !r.projectComparable(substTypeParam(&ast.TypeRef{Named: v.Ref}, subst), pkgName, conservative, seen) {
					return false
				}
			}
		}
	}
	return true
}

// comparableKey renders a stable identity for a type instance — its name plus
// generic args, with array / map structure — for the comparability back-edge
// guard ([typeRefComparable] / [refResolver.projectComparable]). Keying the
// `seen` set by this rather than the bare decl name keeps different
// instantiations of one generic (`Wrap<string>` vs `Wrap<bytes>`) distinct, so
// a comparable instantiation can't poison the guard for a later non-comparable
// one, while a true cycle (the same instantiation) still matches and breaks.
func comparableKey(t *ast.TypeRef) string {
	if t == nil {
		return ""
	}
	if t.Map != nil {
		return "map<" + comparableKey(t.Map.Key) + "," + comparableKey(t.Map.Value) + ">"
	}
	prefix := strings.Repeat("[]", t.ArrayDepth)
	if t.ArrayDepth == 0 && t.Array {
		prefix = "[]"
	}
	if t.Named == nil || t.Named.Name == nil {
		return prefix + "?"
	}
	key := prefix + t.Named.Name.String()
	if len(t.Named.Args) > 0 {
		parts := make([]string, len(t.Named.Args))
		for i, a := range t.Named.Args {
			parts[i] = comparableKey(a)
		}
		key += "<" + strings.Join(parts, ",") + ">"
	}
	return key
}

// checkMapKeysProject walks t for map keys that are a QUALIFIED type JSON
// can't marshal (a bool / float / struct / bytes scalar key) — Go either
// won't compile or panics at json.Marshal. Mirrors
// [analyzer.mapKeysComparable] cross-package.
func (r *refResolver) checkMapKeysProject(t *ast.TypeRef, f *ast.Field) {
	if t == nil {
		return
	}
	if t.Map != nil {
		if k := t.Map.Key; k != nil && k.Named != nil && k.Named.Name != nil && len(k.Named.Name.Parts) == 2 && !r.projectKeyMarshalable(k) {
			r.diag(f.Pos, lexer.SeverityError, CodeMapKeyType,
				"map key %s is not a usable map key: a JSON object key is a string, so encoding/json supports only a string / int* / uint* key (or a scalar / enum over one). An optional (`?`), bool, float, struct, slice, map, bytes, or generic type-parameter key either fails to compile or panics at json.Marshal. Use a non-optional string / int* / uint* / string- or int-scalar / enum key.",
				describeTypeRef(t.Map.Key))
		}
		r.checkMapKeysProject(t.Map.Value, f)
		r.checkMapKeysProject(t.Map.Key, f)
		return
	}
	if t.Array {
		r.checkMapKeysProject(peelOneArray(t), f)
		return
	}
	// A generic instance (`lib.Box<map<lib.FloatKey, V>>`) carries the map
	// inside its type-arg; descend so a non-marshalable qualified key nested
	// in a type-argument is caught too.
	if t.Named != nil {
		for _, arg := range t.Named.Args {
			r.checkMapKeysProject(arg, f)
		}
	}
}

// projectKeyMarshalable reports whether a QUALIFIED map-key type marshals
// to a JSON object key: a string / int* / uint* scalar, or an enum.
func (r *refResolver) projectKeyMarshalable(key *ast.TypeRef) bool {
	if sc := r.lookupScalar(key.Named); sc != nil {
		switch sc.Primitive {
		case "string",
			"int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64":
			return true
		}
		return false // bool / float / bytes scalar
	}
	if r.lookupEnum(key.Named) != nil {
		return true
	}
	// A qualified struct key is never marshalable; an unresolvable name is
	// left to the qualified-ref pass.
	pkgName, sym := splitQualified(key.Named)
	if pkgName != "" {
		if pkg := r.proj.Packages[pkgName]; pkg != nil {
			if _, isType := pkg.Types[sym]; isType {
				return false
			}
		}
	}
	return true
}

func (r *refResolver) checkScalarBoundContradictions(f *ast.Field, prim string) {
	if unsignedPrim(prim) {
		for _, d := range f.Decorators {
			if d == nil {
				continue
			}
			switch d.Name {
			case "negative":
				r.diag(d.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@negative cannot apply to an unsigned type (%s is always >= 0) — every value would be rejected; use a signed integer or drop @negative", prim)
			case "lt":
				if len(d.Args) == 1 {
					if il, ok := d.Args[0].Value.(*ast.IntLit); ok && il.Value == 0 {
						r.diag(d.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
							"@lt(0) cannot apply to an unsigned type (%s is always >= 0) — every value would be rejected; use a signed integer or a positive bound", prim)
					}
				}
			}
		}
	}
	if lo, hi, ok := intCapacity(prim); ok {
		emit := func(d *ast.Decorator, il *ast.IntLit, pos lexer.Position) {
			if v := float64(il.Value); v < lo || v > hi {
				r.diag(pos, lexer.SeverityError, CodeBoundOverflow,
					"@%s bound %d exceeds %s range [%g, %g]", d.Name, il.Value, prim, lo, hi)
			}
		}
		for _, d := range f.Decorators {
			if d == nil {
				continue
			}
			switch d.Name {
			case "gt", "gte", "lt", "lte", "multipleOf":
				if len(d.Args) == 1 {
					if il, ok := d.Args[0].Value.(*ast.IntLit); ok {
						emit(d, il, d.Args[0].Pos)
					}
				}
			case "range":
				for _, ag := range d.Args {
					if il, ok := ag.Value.(*ast.IntLit); ok {
						emit(d, il, ag.Pos)
					}
				}
			}
		}
	}
	// Fractional float bound on an integer scalar (`@lte(2.5)` over
	// `scalar X int`) — the per-package [checkBoundLiteralKind] misses it
	// cross-package for the same local-table reason.
	if integerPrim(prim) {
		for _, d := range f.Decorators {
			if d == nil || d.Name == "multipleOf" {
				// @multipleOf's fractional-divisor rejection is owned by
				// checkMultipleOfTarget (which doesn't depend on the local
				// table), so re-checking it here would double-fire.
				continue
			}
			for _, ag := range d.Args {
				if fl, ok := fractionalArg(ag); ok {
					r.diag(ag.Pos, lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@%s on an integer field requires a whole-number bound, got %g", d.Name, fl.Value)
				}
			}
		}
	}
}

// lookupScalar resolves a qualified `pkg.Name` ref into the
// foreign package's scalar decl, or returns nil when the package
// or symbol is unknown.
func (r *refResolver) lookupScalar(n *ast.NamedTypeRef) *ast.ScalarDecl {
	pkgName, sym := splitQualified(n)
	if pkgName == "" {
		return nil
	}
	pkg := r.proj.Packages[pkgName]
	if pkg == nil {
		return nil
	}
	return pkg.Scalars[sym]
}

func (r *refResolver) lookupEnum(n *ast.NamedTypeRef) *ast.EnumDecl {
	pkgName, sym := splitQualified(n)
	if pkgName == "" {
		return nil
	}
	pkg := r.proj.Packages[pkgName]
	if pkg == nil {
		return nil
	}
	return pkg.Enums[sym]
}

func splitQualified(n *ast.NamedTypeRef) (string, string) {
	if n == nil || n.Name == nil {
		return "", ""
	}
	parts := n.Name.Parts
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func (r *refResolver) diagBinding(d *ast.Decorator, format string, args ...any) {
	r.diag(d.Pos, lexer.SeverityError, CodeBindingType, format, args...)
}

// Keep `strings` import used by file in case future helpers need it.
var _ = strings.Contains
