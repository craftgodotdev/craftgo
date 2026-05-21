// Nested / enum / type-param validator dispatch.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func enumValueCheck(f *ast.Field, pkg *semantic.Package, uses map[string]bool) string {
	if pkg == nil || f == nil || f.Type == nil || f.Type.Map != nil || f.Type.Named == nil {
		return ""
	}
	ed, ok := pkg.Enums[f.Type.Named.Name.String()]
	if !ok || len(ed.EnumValues()) == 0 {
		return ""
	}
	uses["fmt"] = true
	access := "v." + GoFieldName(f.Name)
	caseList := enumCaseList(ed)
	msg := fmt.Sprintf(`"%s: invalid %s value"`, f.Name, ed.Name)
	return shape(f, access, func(elem string) string {
		return fmt.Sprintf(`switch %s {
case %s:
default:
return fmt.Errorf(%s)
}`, elem, caseList, msg)
	})
}

// enumCaseList renders the comma-separated list of fully-qualified
// constant names matching `<EnumName><PascalCase(ValueName)>`, the same
// naming convention `enums.go` uses.
func enumCaseList(ed *ast.EnumDecl) string {
	enumVals := ed.EnumValues()
	parts := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
		parts = append(parts, ed.Name+GoFieldName(v.Name))
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
func nestedValidateCall(f *ast.Field, pkg *semantic.Package) string {
	if pkg == nil || f.Type == nil {
		return ""
	}
	access := "v." + GoFieldName(f.Name)
	body := func(elem string) string {
		return fmt.Sprintf(`if err := %s.Validate(); err != nil {
return err
}`, elem)
	}
	// Map: walk the values. A map value that is a user-defined type
	// (or an array / optional thereof) carries its own Validate().
	// Skipping the walk would leave `map<K, User>` entries unchecked
	// and silently break the recursive-validation contract.
	if f.Type.Map != nil {
		v := f.Type.Map.Value
		if !typeRefHasValidator(v, pkg) {
			return ""
		}
		// Element-access expression for one map value, wrapped in
		// optional / array shape as needed.
		switch {
		case v.Array:
			depth := v.ArrayDepth
			if depth < 1 {
				depth = 1
			}
			inner := emitNestedForLoops("val", depth, body)
			return fmt.Sprintf("for _, val := range %s {\n%s\n}", access, inner)
		case v.Optional:
			return fmt.Sprintf("for _, val := range %s {\nif val != nil {\n%s\n}\n}", access, body("val"))
		default:
			return fmt.Sprintf("for _, val := range %s {\n%s\n}", access, body("val"))
		}
	}
	if f.Type.Named == nil {
		return ""
	}
	if _, ok := pkg.Types[f.Type.Named.Name.String()]; !ok {
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
func typeRefHasValidator(t *ast.TypeRef, pkg *semantic.Package) bool {
	if t == nil || t.Map != nil || t.Named == nil {
		return false
	}
	_, ok := pkg.Types[t.Named.Name.String()]
	return ok
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
