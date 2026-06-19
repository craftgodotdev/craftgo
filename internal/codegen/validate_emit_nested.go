// Nested / enum / type-param validator dispatch.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// enumSwitchBody renders the standard `switch expr { case ... default:
// return ... }` block. Centralised so direct-field, array, optional,
// and map-side emitters share identical output formatting.
func enumSwitchBody(ed *ast.EnumDecl, qualifier, expr, label string) string {
	return fmt.Sprintf(`switch %s {
case %s:
default:
return fmt.Errorf(%s)
}`, expr, enumCaseList(ed, qualifier), fmt.Sprintf(`"%sinvalid %s value"`, errSubject(label), ed.Name))
}

// mapRangeLoop returns the `for ... range m { body }` boilerplate
// for a map walk. The form is gofmt -s aware: when only one side
// is consumed it elides the second loop variable
// (`for key := range m`, `for _, val := range m`) instead of
// emitting `for key, _ := range m` which the simplifier would
// rewrite - keeping `make fmt-check` clean.
func mapRangeLoop(access string, keyHas, valHas bool, body string) string {
	switch {
	case keyHas && valHas:
		return fmt.Sprintf("for key, val := range %s {\n%s\n}", access, body)
	case keyHas:
		return fmt.Sprintf("for key := range %s {\n%s\n}", access, body)
	case valHas:
		return fmt.Sprintf("for _, val := range %s {\n%s\n}", access, body)
	default:
		// Defensive: callers gate on at-least-one side; an empty
		// loop is meaningless and gofmt strips it anyway.
		return ""
	}
}

// enumCaseList renders the comma-separated list of fully-qualified
// constant names, read from the shared enumMembers resolver so the case
// list uses the SAME deduped const names the enum declaration emits - a
// case-colliding enum (`Active` / `active`) yields `EActive, EActive_2`,
// not the `EActive, EActive` a non-deduped walk produced (which failed to
// compile). When the enum lives in a sibling DSL package, qualifier
// carries the Go package prefix (e.g. `"shared."`).
func enumCaseList(ed *ast.EnumDecl, qualifier string) string {
	members := enumMembers(ed)
	parts := make([]string, 0, len(members))
	for _, m := range members {
		parts = append(parts, qualifier+m.ConstName)
	}
	return strings.Join(parts, ", ")
}

// typeParamValidateCall emits the runtime type-assertion path for a
// field whose declared type is a generic parameter (`T`, `T[]`, `T?`).
// Because Go cannot statically prove T has a Validate() method, the
// generated code probes via `any(x).(interface{ Validate() error })`
// and only invokes Validate when the assertion succeeds. Concrete
// instances that happen to satisfy the interface are validated; pure
// primitive instances simply skip the check.
//
// We always pass a *pointer* to the assertion. `Validate()` lands on
// the pointer receiver in our generated code, so `any(value)` would
// miss any concrete struct whose method is declared on `*T`. The shape
// helper hands us the value-form expression for each form; we wrap it
// with `&` for arrays/single, but optional fields are already a `*T`
// so we use the pointer access as-is.
func typeParamValidateCall(f *ast.Field, goName string, uses map[string]bool) string {
	access := "v." + goName
	uses["reflect"] = true
	return shape(f, access, func(elem string) string {
		probe := "&" + elem
		// An optional non-array `T?` lowers to a `*T` whose access is already
		// the pointer to probe. An optional ARRAY (`T[]?`) still iterates
		// per-element, so the element pointer `&elem` is correct - the
		// whole-slice access would never satisfy the Validate() interface.
		if f.Type.Optional && !f.Type.Array {
			probe = access
		}
		// The direct probe handles a T that itself has Validate() (struct /
		// scalar / enum) with no reflection. When it doesn't - T is
		// instantiated as a composite (map / slice) whose ELEMENT carries
		// the constraint - fall back to validateValue, which walks the
		// composite and validates each element. Without the fallback a
		// `Page<map<string, Item>>` advertises Item's constraints in OpenAPI
		// but never enforces them.
		return fmt.Sprintf(`if vv, ok := any(%s).(interface{ Validate() error }); ok {
if err := vv.Validate(); err != nil {
return err
}
} else if err := validateValue(%s); err != nil {
return err
}`, probe, elem)
	})
}

// nestedValidateCall emits a recursive `field.Validate()` call when a
// field's declared type is another user-defined struct (or a generic
// instance, since those carry Validate too). Maps are skipped: map
// values would need range traversal that the codegen does not emit -
// the user must call Validate on map values explicitly when deep
// validation is required.
//
// We bypass the generic [shape] helper for optional fields so the
// emitted call reads `v.Avatar.Validate()` rather than the noisier
// `(*v.Avatar).Validate()` - Go's method-set rules dispatch through
// the pointer-receiver Validate either way, and the cleaner form is
// what a human would write by hand.
// namedIsScalarOrEnum reports whether n names a scalar or enum type - whose
// generated Validate() emits a subject-less message - as opposed to a struct,
// whose Validate() already names its own fields. A field of a scalar/enum type
// wraps the error with the field name (see [validateDispatch]); a struct field
// does not, to avoid a synthetic outer path prefix.
func namedIsScalarOrEnum(n *ast.NamedTypeRef, pkg *semantic.Package, r *ProjectResolver) bool {
	if n == nil || n.Name == nil {
		return false
	}
	name := n.Name.String()
	if _, ok := pkg.Types[name]; ok {
		return false // struct
	}
	if _, ok := pkg.Enums[name]; ok {
		return true
	}
	if _, ok := pkg.Scalars[name]; ok {
		return true
	}
	// Qualified / cross-package: a struct in the project tables wins (no wrap);
	// otherwise treat an enum/scalar as the subject-less case.
	if r.LookupType(name) != nil {
		return false
	}
	return r.LookupEnum(name) != nil || r.LookupScalar(name) != nil
}

// validateDispatch emits the recursive `.Validate()` call for elem. wrapName !=
// "" wraps the error as `<wrapName>: %w` - restoring the field-name subject for a
// scalar/enum whose own message is subject-less; "" returns the error verbatim
// (a struct, which already names its own fields).
func validateDispatch(elem, wrapName string) string {
	if wrapName != "" {
		return fmt.Sprintf("if err := %s.Validate(); err != nil {\nreturn fmt.Errorf(\"%s: %%w\", err)\n}", elem, wrapName)
	}
	return fmt.Sprintf("if err := %s.Validate(); err != nil {\nreturn err\n}", elem)
}

func nestedValidateCall(f *ast.Field, goName string, pkg *semantic.Package, r *ProjectResolver) string {
	if pkg == nil || f.Type == nil {
		return ""
	}
	access := "v." + goName
	// A scalar's / enum's Validate() message is subject-less, so wrap its error
	// with this field's name; a struct's Validate() already names its fields, so
	// its error passes through unwrapped.
	wrapFor := func(n *ast.NamedTypeRef) string {
		if namedIsScalarOrEnum(n, pkg, r) {
			return fieldWireName(f)
		}
		return ""
	}
	// Map: walk both keys AND values. Either side may be a user-
	// defined type (or array / optional thereof on the value side)
	// that carries its own Validate(). Map keys can't be array or
	// optional in the DSL grammar, so the key side stays flat. The
	// walk keeps `map<K, User>` / `map<UserID, V>` (where UserID is a
	// struct-shaped type - uncommon but legal) entries checked,
	// upholding the recursive-validation contract.
	if f.Type.Map != nil {
		k := f.Type.Map.Key
		v := f.Type.Map.Value
		keyHas := typeRefHasValidator(k, pkg, r)
		valHas := typeRefHasValidator(v, pkg, r)
		if !keyHas && !valHas {
			return ""
		}
		// mapWalk ranges ONE map (at mapAccess), calling Validate() on
		// whichever of key / value carries one. Map keys can't be array /
		// optional / map in the grammar, so the key side stays flat; the
		// value side runs through [nestedValueChecks], which handles a value
		// that is itself an array, an optional, or a NESTED map.
		mapWalk := func(mapAccess string) string {
			var stmts []string
			if keyHas {
				stmts = append(stmts, validateDispatch("key", wrapFor(k.Named)))
			}
			if valHas {
				stmts = append(stmts, nestedValueChecks(v, "val", 0, pkg, r, f.Name))
			}
			return mapRangeLoop(mapAccess, keyHas, valHas, strings.Join(stmts, "\n"))
		}
		// `map<K,V>[]` (and deeper, `map<K,V>[][]`) carries BOTH Map and
		// Array on the SAME TypeRef. Peel each array dimension with an index
		// loop first, then walk the map at the leaf - ranging `access`
		// directly would iterate the SLICE as if it were a map and call
		// Validate() on a whole map element.
		if f.Type.Array {
			depth := f.Type.ArrayDepth
			if depth < 1 {
				depth = 1
			}
			return emitNestedForLoops(access, depth, mapWalk)
		}
		return mapWalk(access)
	}
	if f.Type.Named == nil {
		return ""
	}
	if !typeRefNamedHasValidator(f.Type.Named, pkg, r) {
		return ""
	}
	dispatch := func(elem string) string { return validateDispatch(elem, wrapFor(f.Type.Named)) }
	switch {
	case f.Type.Array:
		// Multi-dim arrays (`T[][]`, `T[][][]`) need one for-loop per
		// dimension; a single loop would call `Validate()` on a slice,
		// not the element. ArrayDepth (0 means 1-dim "T[]") drives the
		// nesting depth.
		depth := f.Type.ArrayDepth
		if depth < 1 {
			depth = 1
		}
		return emitNestedForLoops(access, depth, dispatch)
	case fieldNeedsNilGuard(f, pkg, r):
		// The Go field can be nil in the valid "absent / null" state -
		// either a *Type (`?` optional / `@nullable` value type) OR a
		// scalar over a nilable primitive (`scalar Blob bytes` → `[]byte`),
		// which carries no extra pointer but is still nil when absent.
		// Nil-guard before dispatching Validate(), or a nil receiver runs
		// the scalar's own constraints against the empty value and rejects
		// what the OpenAPI null-union advertises as legal. Method dispatch
		// resolves through both shapes, so no explicit deref is needed.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, dispatch(access))
	default:
		return dispatch(access)
	}
}

// typeRefHasValidator reports whether the type referenced by `t`
// (after stripping any array / optional decoration) is a user-defined
// struct that carries a generated Validate() method. Map keys go
// through scalar-decorator emission elsewhere, so this only inspects
// the value side.
func typeRefHasValidator(t *ast.TypeRef, pkg *semantic.Package, r *ProjectResolver) bool {
	if t == nil {
		return false
	}
	if t.Map != nil {
		// A map value can itself be a map (`map<K, map<K2, V>>`): the inner
		// key / value may carry validators that still need walking, so
		// recurse rather than treating every map as validator-free.
		return typeRefHasValidator(t.Map.Key, pkg, r) || typeRefHasValidator(t.Map.Value, pkg, r)
	}
	if t.Named == nil {
		return false
	}
	return typeRefNamedHasValidator(t.Named, pkg, r)
}

// nestedValueChecks recursively emits Validate() dispatch for a value of
// type t reached through `access` - used for a map value, which may itself
// be a nested map, an array, an optional, or a validator-carrying named
// type. depth namespaces the loop variables so nested ranges don't shadow.
// outerName is the using field's name: a scalar/enum leaf (subject-less
// message) is wrapped with it, a struct leaf is not. Returns "" when nothing
// under t carries a validator.
func nestedValueChecks(t *ast.TypeRef, access string, depth int, pkg *semantic.Package, r *ProjectResolver, outerName string) string {
	if t == nil || !typeRefHasValidator(t, pkg, r) {
		return ""
	}
	valErr := func(a string, n *ast.NamedTypeRef) string {
		wrap := ""
		if namedIsScalarOrEnum(n, pkg, r) {
			wrap = outerName
		}
		return validateDispatch(a, wrap)
	}
	switch {
	case t.Array:
		// An array (incl. `map<K,V>[]`, which carries BOTH Array and Map on
		// the same TypeRef): peel ONE array dimension first, then recurse on
		// the element - checking Map before Array would range the slice as a
		// map.
		iv := fmt.Sprintf("i%d", depth)
		elem := *t
		elem.Optional = false
		if elem.ArrayDepth > 0 {
			elem.ArrayDepth--
		}
		if elem.ArrayDepth == 0 {
			elem.Array = false
		}
		return fmt.Sprintf("for %s := range %s {\n%s\n}", iv, access,
			nestedValueChecks(&elem, fmt.Sprintf("%s[%s]", access, iv), depth+1, pkg, r, outerName))
	case t.Map != nil:
		kHas := typeRefHasValidator(t.Map.Key, pkg, r)
		vHas := typeRefHasValidator(t.Map.Value, pkg, r)
		kv := fmt.Sprintf("k%d", depth)
		vv := fmt.Sprintf("v%d", depth)
		var inner []string
		if kHas {
			inner = append(inner, valErr(kv, t.Map.Key.Named))
		}
		if vHas {
			inner = append(inner, nestedValueChecks(t.Map.Value, vv, depth+1, pkg, r, outerName))
		}
		body := strings.Join(inner, "\n")
		switch {
		case kHas && vHas:
			return fmt.Sprintf("for %s, %s := range %s {\n%s\n}", kv, vv, access, body)
		case kHas:
			return fmt.Sprintf("for %s := range %s {\n%s\n}", kv, access, body)
		default:
			return fmt.Sprintf("for _, %s := range %s {\n%s\n}", vv, access, body)
		}
	case t.Optional:
		base := *t
		base.Optional = false
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, nestedValueChecks(&base, access, depth, pkg, r, outerName))
	default:
		return valErr(access, t.Named)
	}
}

// typeRefNamedHasValidator is the named-ref core of typeRefHasValidator,
// shared with [nestedValidateCall] so both map-value walks and direct
// field refs resolve qualified names the same way. A qualified ref
// `shared.Page` lives in the project [TypeTable] (via the
// [ProjectResolver]), not the local `pkg.Types` table, so qualified
// refs consult the resolver to resolve.
func typeRefNamedHasValidator(n *ast.NamedTypeRef, pkg *semantic.Package, r *ProjectResolver) bool {
	if n == nil || n.Name == nil {
		return false
	}
	name := n.Name.String()
	// Local first: a single-part name resolved here matches the
	// receiver-package lookup that pre-existed the resolver plumbing.
	// Structs and enums always carry a Validate(); a scalar carries
	// one only when it declares at least one validator decorator -
	// matching exactly when [buildValidateData] emits the method.
	if _, ok := pkg.Types[name]; ok {
		return true
	}
	if _, ok := pkg.Enums[name]; ok {
		return true
	}
	if sd, ok := pkg.Scalars[name]; ok {
		return scalarDeclHasValidators(sd)
	}
	// Qualified ref → project-wide tables via resolver. nil-safe:
	// the Lookup* helpers on a nil resolver return nil, preserving the
	// single-package behaviour for callers without project context.
	if r.LookupType(name) != nil {
		return true
	}
	if r.LookupEnum(name) != nil {
		return true
	}
	if sd := r.LookupScalar(name); sd != nil {
		return scalarDeclHasValidators(sd)
	}
	return false
}

// emitNestedForLoops produces `depth` nested `for i0 := range x` loops
// where the innermost body sees the deepest element expression
// (`x[i0][i1]...[i{depth-1}]`). Used by [nestedValidateCall] for
// multi-dimensional arrays of struct-typed elements.
func emitNestedForLoops(access string, depth int, body func(elem string) string) string {
	// Build the deepest element path that the body operates on.
	elem := access
	for d := 0; d < depth; d++ {
		elem += fmt.Sprintf("[i%d]", d)
	}
	out := body(elem)
	// Wrap loops outside-in. Loop d ranges over `access[i0]…[i{d-1}]`.
	for d := depth - 1; d >= 0; d-- {
		rangeOver := access
		for k := 0; k < d; k++ {
			rangeOver += fmt.Sprintf("[i%d]", k)
		}
		out = fmt.Sprintf("for i%d := range %s {\n%s\n}", d, rangeOver, out)
	}
	return out
}
