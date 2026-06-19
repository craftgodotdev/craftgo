// Collection shape checks: map-key comparability/marshalability and
// @uniqueItems element comparability, including generic-argument
// substitution.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkMapKeyComparable rejects a map whose key type cannot be a Go map
// key: a bare generic type-parameter (`any`-constrained) or a struct /
// generic that transitively contains a slice / map / bytes. The generated
// `map[K]V` does not compile, yet gen and OpenAPI accept the design (gen
// exits 0, then `go build` fails). Mirrors the @uniqueItems comparability
// guard for the map-key position. Walks nested maps / arrays in the
// field's own type; named types are checked when their own body is walked.
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
	// A generic instance (`Box<map<bad, V>>`) carries the map inside its
	// type-arg; descend so a non-marshalable key nested in a type-argument is
	// caught too, mirroring the @uniqueItems comparability walk.
	if t.Named != nil {
		for _, arg := range t.Named.Args {
			a.mapKeysComparable(arg, f, typeParams)
		}
	}
}

// keyMarshalable reports whether key is a usable Go map key that
// encoding/json can also marshal/unmarshal: a string or integer kind, or a
// scalar / enum over one. Go also COMPILES a bool / float / all-comparable
// struct key, but json.Marshal returns "unsupported type" for those at
// runtime - so they are rejected even though they compile (the OpenAPI
// would advertise a serializable object the server can't produce). A
// qualified cross-package key is accepted conservatively (the project pass
// resolves it); a bare type-parameter is never a valid key.
func (a *analyzer) keyMarshalable(key *ast.TypeRef, typeParams []string) bool {
	if key == nil || key.Named == nil || key.Named.Name == nil || key.Array || key.Map != nil || key.Optional {
		// An optional `?` key would render `map[*K]V`; encoding/json cannot
		// use a pointer as an object key (marshal/unmarshal fail), so it is
		// rejected here regardless of the underlying type. The Optional check
		// precedes the cross-package defer below so a qualified `lib.ID?` key
		// is caught too.
		return false
	}
	name := key.Named.Name.String()
	for _, tp := range typeParams {
		if tp == name {
			return false
		}
	}
	if len(key.Named.Name.Parts) > 1 {
		return true // cross-package ref; defer to the project resolver
	}
	if _, ok := a.pkg.Enums[name]; ok {
		return true // string- or int-backed enum
	}
	prim := name
	if sc, ok := a.pkg.Scalars[name]; ok {
		prim = sc.Primitive
	}
	switch prim {
	case "string",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// checkUniqueItemsComparable rejects `@uniqueItems` on an array whose
// element type is NOT comparable (usable as a Go map key). The runtime
// dedupe loop builds `map[Elem]struct{}`, so a slice / map / `any` /
// `bytes` element - or a struct/generic transitively containing one -
// produces either non-compiling Go (`invalid map key type`) or a runtime
// `hash of unhashable type` panic, while the OpenAPI side still
// advertises `uniqueItems: true`. Catching it at design time keeps the
// generated validator compiling and the spec honest. Element types that
// can't be resolved in this package (cross-package qualified refs) are
// conservatively allowed to avoid false rejections.
func (a *analyzer) checkUniqueItemsComparable(f *ast.Field, typeParams []string) {
	if f == nil || f.Type == nil {
		return
	}
	// @uniqueItems is array-only. A map collapses to PrimArray in the
	// applicability gate (so the gate lets it pass), but neither codegen stage
	// honours it - the validator and OpenAPI both silently drop it. Reject so
	// the constraint can't vanish without a word; a map's keys are unique
	// already and JSON-Schema has no object-uniqueness keyword.
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
		// A type-parameter element (`items T[] @uniqueItems` in a generic
		// decl) is `any`-constrained on the parametric receiver, so the
		// dedupe `map[T]struct{}` cannot compile and the parametric
		// Validate() cannot enforce uniqueness - reject it like any other
		// incomparable element rather than emit non-compiling Go.
		if elem != nil && elem.Named != nil && elem.Named.Name != nil && !elem.Array && elem.Map == nil {
			name := elem.Named.Name.String()
			for _, tp := range typeParams {
				if tp == name {
					a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
						"@uniqueItems is not supported on a type-parameter element (%s): the parametric validator can't build a dedupe map over an `any`-constrained value. Drop @uniqueItems, or use a concrete comparable element type.", name)
					return
				}
			}
		}
		if !a.typeRefComparable(elem, map[string]bool{}) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorTypeMismatch,
				"@uniqueItems requires comparable elements (usable as a map key) - %s is not (a slice / map / `any`, or a struct/generic containing one). Restructure the element into a comparable shape, or drop @uniqueItems.",
				describeTypeRef(elem))
			return
		}
	}
}

// peelOneArray returns the element type after stripping ONE array
// dimension: `Tag[]` -> `Tag` (comparable scalar), `Tag[][]` -> `Tag[]`
// (still an array, hence non-comparable). Mirrors the codegen
// arrayElemType peel so the comparability verdict matches what the
// validator emits. Optional is cleared on the element.
func peelOneArray(t *ast.TypeRef) *ast.TypeRef {
	clone := *t
	clone.Optional = false
	if clone.ArrayDepth > 0 {
		clone.ArrayDepth--
	}
	if clone.ArrayDepth == 0 {
		clone.Array = false
	}
	return &clone
}

// typeRefComparable reports whether values of t are usable as a Go map
// key. Arrays / maps / `any` / `bytes` are not; a named struct or generic
// instance is comparable only when EVERY member is. `seen` guards against
// recursive types (a cycle is treated as comparable along the back-edge).
func (a *analyzer) typeRefComparable(t *ast.TypeRef, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if t.Array || t.Map != nil {
		return false
	}
	if t.Named == nil || t.Named.Name == nil {
		return false
	}
	name := t.Named.Name.String()
	switch name {
	case "any", "bytes", "file":
		return false
	case "string", "bool",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	if sc, ok := a.pkg.Scalars[name]; ok {
		return sc.Primitive != "bytes"
	}
	if _, ok := a.pkg.Enums[name]; ok {
		return true
	}
	if td, ok := a.pkg.Types[name]; ok {
		// Key the back-edge guard by the instantiated identity (name + args),
		// not the bare decl name - otherwise a comparable instantiation
		// (`Wrap<string>`) poisons the guard so a later non-comparable one
		// (`Wrap<bytes>`) short-circuits to "comparable" and leaks a
		// non-compiling dedupe map. A true cycle (same instantiation) still
		// matches and breaks.
		key := comparableKey(t)
		if seen[key] {
			return true
		}
		seen[key] = true
		// For a generic instance (`Pair<bytes>`) substitute the type-args
		// into the decl's fields: a field typed `T` is comparable only if the
		// concrete argument is. Without this, `T` resolves to nothing and
		// falls through to the "conservatively comparable" branch, so
		// `Pair<bytes>[] @uniqueItems` would pass the check and then emit a
		// non-compiling `map[Pair[[]byte]]`.
		subst := map[string]*ast.TypeRef{}
		if len(td.TypeParams) > 0 && t.Named != nil {
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
				// An optional `?` non-collection field renders as a Go pointer
				// (`*T`), which is comparable regardless of what T contains - so
				// it doesn't break the struct's usability as a map key. Don't
				// descend past the pointer. (Optional arrays/maps stay non-
				// comparable: they render as a nil-able slice/map, not a pointer.)
				if ft != nil && ft.Optional && !ft.Array && ft.Map == nil {
					continue
				}
				if !a.typeRefComparable(ft, seen) {
					return false
				}
			case *ast.Mixin:
				if v.Ref != nil && v.Ref.Name != nil {
					// Substitute the outer decl's type-args into the mixin ref
					// too (a generic mixin `Inner<T>` becomes `Inner<bytes>`),
					// mirroring the Field branch above - without it a bare `T`
					// inside the mixin escapes the comparability check.
					if !a.typeRefComparable(substTypeParam(&ast.TypeRef{Named: v.Ref}, subst), seen) {
						return false
					}
				}
			}
		}
		return true
	}
	// Unresolved here (cross-package qualified ref or bare generic
	// type-param) - conservatively comparable to avoid a false reject.
	return true
}

// substTypeParam replaces a bare type-parameter reference (`T`) with its
// concrete argument from subst, and recurses into a nested generic
// instance's args (`Inner<T>`). Array / map fields are returned unchanged
// - they are non-comparable regardless of the element, so the caller
// rejects them before any substitution matters.
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
