package golang

import (
	"fmt"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// validateCalls renders the Validate() calls field t's value needs: on each
// value of a type with a generated Validate(), and a run-time probe on each
// value of one of params, the enclosing type's type parameters.
func validateCalls(t checkTarget, params []string, ctx emitCtx) string {
	typ := *t.typ
	// A @nullable value is nil when absent, as an optional one is.
	typ.Optional = t.nilGuard
	return validateWalk{ctx: ctx, params: params, subject: t.subject}.walk(&typ, t.access, 0)
}

// validateWalk renders the Validate() calls of one field's value, through its
// array dimensions, map keys and values and nil optionals.
type validateWalk struct {
	ctx     emitCtx
	params  []string // type parameters, whose values are probed at run time
	subject string   // the field's name, which prefixes each call's error
}

// walk renders the calls a value of type t held in access needs; depth keeps
// the variables of nested loops apart.
func (w validateWalk) walk(t *ast.TypeRef, access string, depth int) string {
	if !w.needs(t) {
		return ""
	}
	switch {
	case t.Array:
		i := fmt.Sprintf("i%d", depth)
		return fmt.Sprintf("for %s := range %s {\n%s\n}", i, access, w.walk(t.ElemTypeRef(), access+"["+i+"]", depth+1))
	case t.Map != nil:
		key, val := fmt.Sprintf("key%d", depth), fmt.Sprintf("val%d", depth)
		keyCalls := w.walk(t.Map.Key, key, depth+1)
		valCalls := w.walk(t.Map.Value, val, depth+1)
		switch {
		case valCalls == "":
			return fmt.Sprintf("for %s := range %s {\n%s\n}", key, access, keyCalls)
		case keyCalls == "":
			return fmt.Sprintf("for _, %s := range %s {\n%s\n}", val, access, valCalls)
		}
		return fmt.Sprintf("for %s, %s := range %s {\n%s\n%s\n}", key, val, access, keyCalls, valCalls)
	case t.Optional:
		// The Go value is a pointer unless its type holds nil itself.
		ptr := !w.ctx.resolver.ResolveTypeRef(t).IsNilable
		return guardBlock(access, w.leaf(t.Named, access, ptr))
	}
	return w.leaf(t.Named, access, false)
}

// needs reports whether a value of type t gets a call: it, or a key or value
// of a map t, names a type parameter or a type with a generated Validate().
func (w validateWalk) needs(t *ast.TypeRef) bool {
	if t.Map != nil {
		return w.needs(t.Map.Key) || w.needs(t.Map.Value)
	}
	if t.Named == nil {
		return false
	}
	return slices.Contains(w.params, t.Named.Name.String()) || typeRefNamedHasValidator(t.Named, w.ctx)
}

// leaf renders the call on one value of named type n held in access, a pointer
// to the value when ptr: the value's Validate(), or the run-time probe of a
// type parameter's.
func (w validateWalk) leaf(n *ast.NamedTypeRef, access string, ptr bool) string {
	if slices.Contains(w.params, n.Name.String()) {
		return w.probe(access, ptr)
	}
	return fmt.Sprintf("if err := %s.Validate(); err != nil {\n%s\n}", access, w.returnErr())
}

// probe renders the run-time check of a type parameter's value held in
// access, a pointer to it when ptr: its Validate() through a pointer (a
// struct's has a pointer receiver), else a validateValue walk.
func (w validateWalk) probe(access string, ptr bool) string {
	w.ctx.imports.use("reflect")
	addr := "&" + access
	if ptr {
		addr = access
	}
	ret := w.returnErr()
	return fmt.Sprintf(`if vv, ok := any(%s).(interface{ Validate() error }); ok {
if err := vv.Validate(); err != nil {
%s
}
} else if err := validateValue(%s); err != nil {
%s
}`, addr, ret, access, ret)
}

// returnErr renders the return of a call's err, `<subject>: %w`.
func (w validateWalk) returnErr() string {
	w.ctx.imports.use("fmt")
	return fmt.Sprintf("return fmt.Errorf(\"%s: %%w\", err)", escapeErrorf(w.subject))
}

// typeRefNamedHasValidator reports whether n names a type whose generated
// Validate() can fail: any struct or enum, or a scalar with a check its
// primitive does not already enforce.
func typeRefNamedHasValidator(n *ast.NamedTypeRef, ctx emitCtx) bool {
	if n == nil || n.Name == nil {
		return false
	}
	name := n.Name.String()
	if ctx.resolver.LookupType(name) != nil || ctx.resolver.LookupEnum(name) != nil {
		return true
	}
	if sd := ctx.resolver.LookupScalar(name); sd != nil {
		return scalarChecksCanFail(sd, ctx)
	}
	return false
}
