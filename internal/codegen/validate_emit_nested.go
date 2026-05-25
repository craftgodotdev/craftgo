// Nested / enum / type-param validator dispatch.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func enumValueCheck(f *ast.Field, pkg *semantic.Package, enums EnumTable, crossPkg CrossPkg, uses map[string]bool) string {
	if pkg == nil || f == nil || f.Type == nil {
		return ""
	}
	access := "v." + GoFieldName(f.Name)
	// Map case: walk both sides independently. A map slot may carry
	// an enum on either the key OR the value (or both); we only emit
	// a range loop when at least one side resolves. Map keys / values
	// can't be array-or-optional in the DSL, so the per-side body is
	// always a direct switch — no shape() wrapping needed.
	if f.Type.Map != nil {
		var keyEd, valEd *ast.EnumDecl
		var keyQual, valQual string
		if k := f.Type.Map.Key; k != nil && k.Named != nil {
			keyEd, keyQual = resolveEnumNamed(k.Named, pkg, enums, crossPkg, uses)
		}
		if v := f.Type.Map.Value; v != nil && v.Named != nil {
			valEd, valQual = resolveEnumNamed(v.Named, pkg, enums, crossPkg, uses)
		}
		if keyEd == nil && valEd == nil {
			return ""
		}
		uses["fmt"] = true
		var stmts []string
		if keyEd != nil {
			stmts = append(stmts, enumSwitchBody(keyEd, keyQual, "key", f.Name+" key"))
		}
		if valEd != nil {
			stmts = append(stmts, enumSwitchBody(valEd, valQual, "val", f.Name+" value"))
		}
		return mapRangeLoop(access, keyEd != nil, valEd != nil, strings.Join(stmts, "\n"))
	}
	if f.Type.Named == nil {
		return ""
	}
	ed, qualifier := resolveEnumNamed(f.Type.Named, pkg, enums, crossPkg, uses)
	if ed == nil || len(ed.EnumValues()) == 0 {
		return ""
	}
	uses["fmt"] = true
	return shape(f, access, func(elem string) string {
		return enumSwitchBody(ed, qualifier, elem, f.Name)
	})
}

// resolveEnumNamed returns (EnumDecl, qualifier) when the named ref
// resolves to an enum reachable from the package being generated.
// qualifier carries the Go package prefix (e.g. `"shared."`) for
// cross-package enums and is empty for local ones — so the emitted
// case list stays bare for the long-standing single-package shape
// and prefixes only when the constants live in a sibling package.
// Side effect: when the ref is cross-package, the resolver also
// registers the target's Go import path in uses, so validate.go can
// reference `shared.ColorRed` and friends.
func resolveEnumNamed(n *ast.NamedTypeRef, pkg *semantic.Package, enums EnumTable, crossPkg CrossPkg, uses map[string]bool) (*ast.EnumDecl, string) {
	if n == nil || n.Name == nil {
		return nil, ""
	}
	name := n.Name.String()
	if ed, ok := pkg.Enums[name]; ok {
		return ed, ""
	}
	if enums == nil {
		return nil, ""
	}
	ed, ok := enums[name]
	if !ok {
		return nil, ""
	}
	parts := n.Name.Parts
	if len(parts) != 2 {
		return ed, ""
	}
	qualifier := parts[0] + "."
	if crossPkg != nil {
		if path := crossPkg[parts[0]]; path != "" {
			uses[path] = true
		}
	}
	return ed, qualifier
}

// enumSwitchBody renders the standard `switch expr { case ... default:
// return ... }` block. Centralised so direct-field, array, optional,
// and map-side emitters share identical output formatting.
func enumSwitchBody(ed *ast.EnumDecl, qualifier, expr, label string) string {
	return fmt.Sprintf(`switch %s {
case %s:
default:
return fmt.Errorf(%s)
}`, expr, enumCaseList(ed, qualifier), fmt.Sprintf(`"%s: invalid %s value"`, label, ed.Name))
}

// mapRangeLoop returns the `for ... range m { body }` boilerplate
// for a map walk. The form is gofmt -s aware: when only one side
// is consumed it elides the second loop variable
// (`for key := range m`, `for _, val := range m`) instead of
// emitting `for key, _ := range m` which the simplifier would
// rewrite — keeping `make fmt-check` clean.
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
// constant names matching `<EnumName><PascalCase(ValueName)>`, the same
// naming convention `enums.go` uses. When the enum lives in a sibling
// DSL package, qualifier carries the Go package prefix (e.g.
// `"shared."`) so the case list compiles against the cross-package
// constants.
func enumCaseList(ed *ast.EnumDecl, qualifier string) string {
	enumVals := ed.EnumValues()
	parts := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
		parts = append(parts, qualifier+ed.Name+GoFieldName(v.Name))
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
func typeParamValidateCall(f *ast.Field) string {
	access := "v." + GoFieldName(f.Name)
	return shape(f, access, func(elem string) string {
		probe := "&" + elem
		if f.Type.Optional {
			probe = access
		}
		return fmt.Sprintf(`if vv, ok := any(%s).(interface{ Validate() error }); ok {
if err := vv.Validate(); err != nil {
return err
}
}`, probe)
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
func nestedValidateCall(f *ast.Field, pkg *semantic.Package, projTypes TypeTable) string {
	if pkg == nil || f.Type == nil {
		return ""
	}
	access := "v." + GoFieldName(f.Name)
	body := func(elem string) string {
		return fmt.Sprintf(`if err := %s.Validate(); err != nil {
return err
}`, elem)
	}
	// Map: walk both keys AND values. Either side may be a user-
	// defined type (or array / optional thereof on the value side)
	// that carries its own Validate(). Map keys can't be array or
	// optional in the DSL grammar, so the key side stays flat.
	// Skipping the walk would leave `map<K, User>` / `map<UserID, V>`
	// (where UserID is a struct-shaped type — uncommon but legal)
	// entries unchecked and silently break the recursive-validation
	// contract.
	if f.Type.Map != nil {
		k := f.Type.Map.Key
		v := f.Type.Map.Value
		keyHas := typeRefHasValidator(k, pkg, projTypes)
		valHas := typeRefHasValidator(v, pkg, projTypes)
		if !keyHas && !valHas {
			return ""
		}
		var stmts []string
		if keyHas {
			stmts = append(stmts, body("key"))
		}
		if valHas {
			// Value-side shape: arrays of struct elements need nested
			// for-loops; optionals need a nil-guard; otherwise call
			// directly.
			switch {
			case v.Array:
				depth := v.ArrayDepth
				if depth < 1 {
					depth = 1
				}
				stmts = append(stmts, emitNestedForLoops("val", depth, body))
			case v.Optional:
				stmts = append(stmts, fmt.Sprintf("if val != nil {\n%s\n}", body("val")))
			default:
				stmts = append(stmts, body("val"))
			}
		}
		return mapRangeLoop(access, keyHas, valHas, strings.Join(stmts, "\n"))
	}
	if f.Type.Named == nil {
		return ""
	}
	if !typeRefNamedHasValidator(f.Type.Named, pkg, projTypes) {
		return ""
	}
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
		return emitNestedForLoops(access, depth, body)
	case f.Type.Optional:
		// access is already `*Type`. Method dispatch auto-resolves
		// through the pointer; no explicit deref needed.
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, body(access))
	default:
		return body(access)
	}
}

// typeRefHasValidator reports whether the type referenced by `t`
// (after stripping any array / optional decoration) is a user-defined
// struct that carries a generated Validate() method. Map keys go
// through scalar-decorator emission elsewhere, so this only inspects
// the value side.
func typeRefHasValidator(t *ast.TypeRef, pkg *semantic.Package, projTypes TypeTable) bool {
	if t == nil || t.Map != nil || t.Named == nil {
		return false
	}
	return typeRefNamedHasValidator(t.Named, pkg, projTypes)
}

// typeRefNamedHasValidator is the named-ref core of typeRefHasValidator,
// shared with [nestedValidateCall] so both map-value walks and direct
// field refs resolve qualified names the same way. A qualified ref
// `shared.Page` lives in the project [TypeTable], not the local
// `pkg.Types` table — without the table consult, qualified refs were
// dropped silently (the local lookup never finds them).
func typeRefNamedHasValidator(n *ast.NamedTypeRef, pkg *semantic.Package, projTypes TypeTable) bool {
	if n == nil || n.Name == nil {
		return false
	}
	name := n.Name.String()
	// Local first: a single-part name resolved here matches the
	// receiver-package lookup that pre-existed the TypeTable plumbing.
	if _, ok := pkg.Types[name]; ok {
		return true
	}
	// Qualified ref → project-wide table. nil-safe: passing nil
	// projTypes preserves the single-package behaviour callers without
	// project context expect.
	if projTypes != nil {
		_, ok := projTypes[name]
		return ok
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
