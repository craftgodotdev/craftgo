package semantic

import (
	"slices"

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
				t.Map.Key.String())
		}
		a.mapKeysComparable(t.Map.Value, f, typeParams)
		a.mapKeysComparable(t.Map.Key, f, typeParams)
		return
	}
	if t.Array {
		a.mapKeysComparable(t.ElemTypeRef(), f, typeParams)
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
// a non-optional string or integer, or a scalar or enum over one. A name
// that resolves to no type is left to the reference check.
func (a *analyzer) keyMarshalable(key *ast.TypeRef, typeParams []string) bool {
	if key == nil || key.Named == nil || key.Named.Name == nil || key.Array || key.Optional {
		return false
	}
	if slices.Contains(typeParams, key.Named.Name.String()) {
		return false
	}
	rt := resolveTypeRef(key, false, a.pkg, a.proj)
	switch rt.Category {
	case CatEnum, CatUnknown:
		return true
	case CatPrimitive, CatScalar:
		switch sp, _ := prims.Lookup(rt.ResolvedPrim); sp.Kind {
		case prims.String, prims.Int, prims.Uint:
			return true
		}
	}
	return false
}

// checkUniqueItemsComparable rejects `@uniqueItems` on an array whose
// elements the validator cannot dedupe by value.
func (a *analyzer) checkUniqueItemsComparable(f *ast.Field, typeParams []string) {
	d := ast.FindDecorator(f.Decorators, "uniqueItems")
	if d == nil || f.Type == nil || !f.Type.Array {
		return
	}
	elem := f.Type.ElemTypeRef()
	if !elem.Array && elem.Named != nil && elem.Named.Name != nil && slices.Contains(typeParams, elem.Named.Name.String()) {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
			"@uniqueItems is not supported on a type-parameter element (%s): the parametric validator can't build a dedupe map over an `any`-constrained value. Drop @uniqueItems, or use a concrete comparable element type.", elem.Named.Name)
		return
	}
	if problem := a.dedupeKeyProblem(elem, a.pkg.Name, "", map[string]bool{}); problem != "" {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
			"@uniqueItems needs elements the validator compares by value, as map keys: %s. Restructure the element, or drop @uniqueItems.", problem)
	}
}

// dedupeKeyProblem says why values of t, spelled as package view spells it,
// cannot key the dedupe map by value, or returns "" when they can; path
// names t within the element, "" for the element itself.
func (a *analyzer) dedupeKeyProblem(t *ast.TypeRef, view, path string, seen map[string]bool) string {
	rt := resolveTypeRef(t, false, a.proj.Packages[view], a.proj)
	switch rt.Category {
	case CatPrimitive:
		if sp, _ := prims.Lookup(rt.ResolvedPrim); sp.Kind == prims.DateTime {
			return dedupeSubject(path, t) + " is a datetime, a time.Time that carries its location: two equal instants in different zones compare unequal"
		}
		return ""
	case CatEnum, CatUnknown:
		return ""
	case CatScalar:
		if !rt.IsNilable {
			return ""
		}
	case CatFile:
		return dedupeSubject(path, t) + " is a pointer the validator compares by address"
	case CatStruct:
		return a.structDedupeProblem(t, view, path, seen)
	}
	return dedupeSubject(path, t) + " is not comparable"
}

// structDedupeProblem is [analyzer.dedupeKeyProblem] for a struct instance:
// every member, mixin members included, must be compared by value, and no
// member may be a pointer. A revisited instance is a cycle and passes.
func (a *analyzer) structDedupeProblem(t *ast.TypeRef, view, path string, seen map[string]bool) string {
	key := t.String()
	if seen[key] {
		return ""
	}
	seen[key] = true
	pkg, sym := a.proj.resolve(view, t.Named.Name)
	if pkg == nil || pkg.Types[sym] == nil {
		return ""
	}
	td := pkg.Types[sym]
	if path == "" {
		path = t.String()
	}
	fields, _ := a.proj.flattenFields(view, pkg.Name, td.Body, td.TypeParams, t.Named.Args, nil)
	for _, ff := range fields {
		m := ff.Field
		member := path + "." + m.Name
		if ResolveField(m, a.proj.Packages[view], a.proj).GoPointer() {
			return dedupeSubject(member, m.Type) + " is a pointer the validator compares by address"
		}
		if problem := a.dedupeKeyProblem(m.Type, view, member, seen); problem != "" {
			return problem
		}
	}
	return ""
}

// dedupeSubject names t at path for a @uniqueItems diagnostic.
func dedupeSubject(path string, t *ast.TypeRef) string {
	if path == "" {
		return t.String()
	}
	return path + " (" + t.String() + ")"
}
