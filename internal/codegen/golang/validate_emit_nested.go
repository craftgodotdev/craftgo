package golang

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// mapRangeLoop ranges access binding only the sides the body uses (`key`,
// `val`), in the form gofmt -s leaves alone.
func mapRangeLoop(access string, keyHas, valHas bool, body string) string {
	switch {
	case keyHas && valHas:
		return fmt.Sprintf("for key, val := range %s {\n%s\n}", access, body)
	case keyHas:
		return fmt.Sprintf("for key := range %s {\n%s\n}", access, body)
	case valHas:
		return fmt.Sprintf("for _, val := range %s {\n%s\n}", access, body)
	default:
		return ""
	}
}

// typeParamValidateCall probes a type-parameter field held in t through a
// pointer (a struct's Validate() has a pointer receiver), else walks it with
// validateValue.
func typeParamValidateCall(t checkTarget, ctx emitCtx) string {
	ctx.uses["reflect"] = true
	return shape(t, func(elem string) string {
		probe := "&" + elem
		// A `T?` field is already the pointer to probe.
		if t.typ.Optional && t.cat != semantic.CatArray {
			probe = t.access
		}
		return fmt.Sprintf(`if vv, ok := any(%s).(interface{ Validate() error }); ok {
if err := vv.Validate(); err != nil {
return err
}
} else if err := validateValue(%s); err != nil {
return err
}`, probe, elem)
	})
}

// namedIsScalarOrEnum reports whether n names a scalar or an enum, whose
// Validate() error has no subject and so is wrapped with the using field's name.
func namedIsScalarOrEnum(n *ast.NamedTypeRef, ctx emitCtx) bool {
	if n == nil || n.Name == nil {
		return false
	}
	name := n.Name.String()
	if ctx.resolver.LookupType(name) != nil {
		return false // struct
	}
	return ctx.resolver.LookupEnum(name) != nil || ctx.resolver.LookupScalar(name) != nil
}

// validateDispatch calls elem.Validate(), wrapping its error as `<wrapName>: %w`
// when wrapName is set.
func validateDispatch(elem, wrapName string, ctx emitCtx) string {
	if wrapName != "" {
		ctx.uses["fmt"] = true
		return fmt.Sprintf("if err := %s.Validate(); err != nil {\nreturn fmt.Errorf(\"%s: %%w\", err)\n}", elem, wrapName)
	}
	return fmt.Sprintf("if err := %s.Validate(); err != nil {\nreturn err\n}", elem)
}

// nestedValidateCall calls Validate() on a field held in t whose type has one,
// through array dimensions and map keys and values; a nil optional is skipped.
// A map value's scalar or enum error names dslName.
func nestedValidateCall(t checkTarget, dslName string, ctx emitCtx) string {
	access := t.access
	wrapFor := func(n *ast.NamedTypeRef) string {
		if namedIsScalarOrEnum(n, ctx) {
			return t.subject
		}
		return ""
	}
	typ := t.typ
	if typ.Map != nil {
		k := typ.Map.Key
		v := typ.Map.Value
		keyHas := typeRefHasValidator(k, ctx)
		valHas := typeRefHasValidator(v, ctx)
		if !keyHas && !valHas {
			return ""
		}
		// The grammar keeps a map key flat; only the value side can nest.
		mapWalk := func(mapAccess string) string {
			var stmts []string
			if keyHas {
				stmts = append(stmts, validateDispatch("key", wrapFor(k.Named), ctx))
			}
			if valHas {
				stmts = append(stmts, nestedValueChecks(v, "val", 0, ctx, dslName))
			}
			return mapRangeLoop(mapAccess, keyHas, valHas, strings.Join(stmts, "\n"))
		}
		// `map<K,V>[]` sets both Map and Array: loop the array dimensions first.
		if typ.Array {
			depth := typ.ArrayDepth
			if depth < 1 {
				depth = 1
			}
			return emitNestedForLoops(access, depth, mapWalk)
		}
		return mapWalk(access)
	}
	if typ.Named == nil {
		return ""
	}
	if !typeRefNamedHasValidator(typ.Named, ctx) {
		return ""
	}
	dispatch := func(elem string) string { return validateDispatch(elem, wrapFor(typ.Named), ctx) }
	switch {
	case typ.Array:
		depth := typ.ArrayDepth
		if depth < 1 {
			depth = 1
		}
		return emitNestedForLoops(access, depth, dispatch)
	case t.nilGuard:
		// Nil is the valid absent/null value, pointer or not (`scalar Blob bytes`).
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, dispatch(access))
	default:
		return dispatch(access)
	}
}

// typeRefHasValidator reports whether t, or a key or value of a map t, names a
// type with a generated Validate().
func typeRefHasValidator(t *ast.TypeRef, ctx emitCtx) bool {
	if t == nil {
		return false
	}
	if t.Map != nil {
		return typeRefHasValidator(t.Map.Key, ctx) || typeRefHasValidator(t.Map.Value, ctx)
	}
	if t.Named == nil {
		return false
	}
	return typeRefNamedHasValidator(t.Named, ctx)
}

// nestedValueChecks validates a map value of type t at access through arrays,
// optionals and nested maps; depth keeps the loop variables of nested ranges
// apart, and outerName wraps a scalar or enum error.
func nestedValueChecks(t *ast.TypeRef, access string, depth int, ctx emitCtx, outerName string) string {
	if t == nil || !typeRefHasValidator(t, ctx) {
		return ""
	}
	valErr := func(a string, n *ast.NamedTypeRef) string {
		wrap := ""
		if namedIsScalarOrEnum(n, ctx) {
			wrap = outerName
		}
		return validateDispatch(a, wrap, ctx)
	}
	switch {
	case t.Array:
		// Before the Map case: `map<K,V>[]` sets both.
		iv := fmt.Sprintf("i%d", depth)
		elem := t.ElemTypeRef()
		return fmt.Sprintf("for %s := range %s {\n%s\n}", iv, access,
			nestedValueChecks(elem, fmt.Sprintf("%s[%s]", access, iv), depth+1, ctx, outerName))
	case t.Map != nil:
		kHas := typeRefHasValidator(t.Map.Key, ctx)
		vHas := typeRefHasValidator(t.Map.Value, ctx)
		kv := fmt.Sprintf("k%d", depth)
		vv := fmt.Sprintf("v%d", depth)
		var inner []string
		if kHas {
			inner = append(inner, valErr(kv, t.Map.Key.Named))
		}
		if vHas {
			inner = append(inner, nestedValueChecks(t.Map.Value, vv, depth+1, ctx, outerName))
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
		return fmt.Sprintf("if %s != nil {\n%s\n}", access, nestedValueChecks(&base, access, depth, ctx, outerName))
	default:
		return valErr(access, t.Named)
	}
}

// typeRefNamedHasValidator reports whether n names a type with a generated
// Validate(): any struct or enum, or a scalar with validators.
func typeRefNamedHasValidator(n *ast.NamedTypeRef, ctx emitCtx) bool {
	if n == nil || n.Name == nil {
		return false
	}
	name := n.Name.String()
	if ctx.resolver.LookupType(name) != nil || ctx.resolver.LookupEnum(name) != nil {
		return true
	}
	if sd := ctx.resolver.LookupScalar(name); sd != nil {
		return scalarDeclHasValidators(sd)
	}
	return false
}

// emitNestedForLoops wraps body in depth nested index loops (i0, i1, ...) over
// access and hands it the innermost element.
func emitNestedForLoops(access string, depth int, body func(elem string) string) string {
	elem := access
	for d := 0; d < depth; d++ {
		elem += fmt.Sprintf("[i%d]", d)
	}
	out := body(elem)
	for d := depth - 1; d >= 0; d-- {
		rangeOver := access
		for k := 0; k < d; k++ {
			rangeOver += fmt.Sprintf("[i%d]", k)
		}
		out = fmt.Sprintf("for i%d := range %s {\n%s\n}", d, rangeOver, out)
	}
	return out
}
