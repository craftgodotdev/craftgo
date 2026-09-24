package semantic

import (
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkMapKeyComparable rejects a map in f's type whose key encoding/json
// cannot marshal; maps inside named types are checked at their declaration.
func (a *analyzer) checkMapKeyComparable(f *ast.Field, typeParams []string) {
	if f == nil {
		return
	}
	a.mapKeysComparable(f.Type, f, typeParams)
}

func (a *analyzer) mapKeysComparable(t *ast.TypeRef, f *ast.Field, typeParams []string) {
	if t == nil {
		return
	}
	if t.Map != nil {
		if !a.keyMarshalable(t.Map.Key, typeParams) {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeMapKeyType,
				"map key %s is not a usable map key: a JSON object key is a string, so encoding/json supports only a string / int* / uint* key (or a scalar / enum over one). An optional (`?`), bool, float, struct, slice, map, bytes, or generic type-parameter key either fails to compile or panics at json.Marshal. Use a non-optional string / int* / uint* / string- or int-scalar / enum key.",
				describeTypeRef(t.Map.Key))
		}
		a.mapKeysComparable(t.Map.Value, f, typeParams)
		a.mapKeysComparable(t.Map.Key, f, typeParams)
		return
	}
	if t.Array {
		a.mapKeysComparable(peelOneArray(t), f, typeParams)
		return
	}
	// Type arguments can hold maps too: `Box<map<K, V>>`.
	if t.Named != nil {
		for _, arg := range t.Named.Args {
			a.mapKeysComparable(arg, f, typeParams)
		}
	}
}

// keyMarshalable reports whether encoding/json accepts key as an object key:
// a non-optional string or integer, or a scalar or enum over one.
func (a *analyzer) keyMarshalable(key *ast.TypeRef, typeParams []string) bool {
	if key == nil || key.Named == nil || key.Named.Name == nil || key.Array || key.Map != nil || key.Optional {
		return false
	}
	if slices.Contains(typeParams, key.Named.Name.String()) {
		return false
	}
	if a.lookupEnum(key.Named) != nil {
		return true // string- or int-backed enum
	}
	if sp, ok := prims.Lookup(a.primOf(key)); ok {
		switch sp.Kind {
		case prims.String, prims.Int, prims.Uint:
			return true
		}
	}
	if isQualifiedTypeRef(key) {
		pkg, sym := a.resolveNamed(a.pkg.Name, key.Named)
		if pkg == nil || !packageHasSymbol(pkg, sym) {
			return true
		}
	}
	return false
}

// checkUniqueItemsComparable rejects `@uniqueItems` on a map, and on an
// array whose element cannot key the Go map the validator dedupes with.
func (a *analyzer) checkUniqueItemsComparable(f *ast.Field, typeParams []string) {
	if f == nil || f.Type == nil {
		return
	}
	// A map passes the PrimArray gate but has no @uniqueItems form.
	if f.Type.Map != nil {
		if d := ast.FindDecorator(f.Decorators, "uniqueItems"); d != nil {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@uniqueItems applies to array fields, not maps (field %q): a map's keys are already unique and there is no object-uniqueness form. Drop @uniqueItems.", f.Name)
		}
		return
	}
	if !f.Type.Array {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != "uniqueItems" {
			continue
		}
		elem := peelOneArray(f.Type)
		if elem != nil && elem.Named != nil && elem.Named.Name != nil && !elem.Array && elem.Map == nil {
			if name := elem.Named.Name.String(); slices.Contains(typeParams, name) {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
					"@uniqueItems is not supported on a type-parameter element (%s): the parametric validator can't build a dedupe map over an `any`-constrained value. Drop @uniqueItems, or use a concrete comparable element type.", name)
				return
			}
		}
		if !a.typeRefComparable(elem, a.pkg.Name, map[string]bool{}) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@uniqueItems requires comparable elements (usable as a map key) - %s is not (a slice / map / `any`, or a struct/generic containing one). Restructure the element into a comparable shape, or drop @uniqueItems.",
				describeTypeRef(elem))
			return
		}
	}
}

// peelOneArray returns t with one array dimension and its `?` removed.
func peelOneArray(t *ast.TypeRef) *ast.TypeRef {
	return t.ElemTypeRef()
}

// typeRefComparable reports whether t can key a Go map, resolving bare names
// in homePkg; a struct is comparable when all its members are.
func (a *analyzer) typeRefComparable(t *ast.TypeRef, homePkg string, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if t.Array || t.Map != nil {
		return false
	}
	if t.Named == nil || t.Named.Name == nil {
		return false
	}
	if sp, ok := prims.Lookup(t.Named.Name.String()); ok {
		switch sp.Kind {
		case prims.Any, prims.Bytes, prims.File:
			return false
		case prims.String, prims.Bool, prims.Int, prims.Uint, prims.Float, prims.DateTime:
			return true
		}
	}
	pkg, sym := a.resolveNamed(homePkg, t.Named)
	if pkg == nil {
		return true // unknown package - reported by the reference pass
	}
	if sc, ok := pkg.Scalars[sym]; ok {
		return sc.Primitive != "bytes"
	}
	if _, ok := pkg.Enums[sym]; ok {
		return true
	}
	td, ok := pkg.Types[sym]
	if !ok {
		// A type parameter or an unresolved name passes.
		return true
	}
	// Keyed per instantiation, so Wrap<string> cannot vouch for Wrap<bytes>;
	// a revisited instantiation is a cycle and passes.
	key := pkg.Name + "." + comparableKey(t)
	if seen[key] {
		return true
	}
	seen[key] = true
	// A member typed T is as comparable as the instance's argument for T.
	subst := map[string]*ast.TypeRef{}
	for i, tp := range td.TypeParams {
		if i < len(t.Named.Args) {
			subst[tp] = t.Named.Args[i]
		}
	}
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			ft := substTypeParam(v.Type, subst)
			// An optional non-collection member is taken to render as a
			// pointer, which is comparable.
			if ft != nil && ft.Optional && !ft.Array && ft.Map == nil {
				continue
			}
			if !a.typeRefComparable(ft, pkg.Name, seen) {
				return false
			}
		case *ast.Mixin:
			if v.Ref != nil && v.Ref.Name != nil {
				// A generic mixin takes the instance's arguments too.
				if !a.typeRefComparable(substTypeParam(&ast.TypeRef{Named: v.Ref}, subst), pkg.Name, seen) {
					return false
				}
			}
		}
	}
	return true
}

// substTypeParam replaces a non-array t named by a type parameter with its
// subst entry, and substitutes into generic arguments recursively.
func substTypeParam(t *ast.TypeRef, subst map[string]*ast.TypeRef) *ast.TypeRef {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return t
	}
	if !t.Array && t.Map == nil {
		if rep, ok := subst[t.Named.Name.String()]; ok {
			return rep
		}
	}
	if len(t.Named.Args) > 0 {
		clone := *t
		nn := *t.Named
		nn.Args = make([]*ast.TypeRef, len(t.Named.Args))
		for i, arg := range t.Named.Args {
			nn.Args[i] = substTypeParam(arg, subst)
		}
		clone.Named = &nn
		return &clone
	}
	return t
}

// comparableKey renders t's name, generic arguments and array or map shape
// as the cycle key of the comparability walk.
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
