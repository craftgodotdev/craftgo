package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// Resolving a type's fields into the layer-agnostic IR: mixins flattened,
// generic arguments substituted, request auto-binding applied. A target
// supplies how it names each level's fields and adds its own rendering on
// top; the facts themselves are resolved once here.

// LevelNames returns the identifiers a target renders one struct level's
// fields with, in body order. Nil leaves [ResolvedField.Name] empty.
type LevelNames func([]ast.TypeMember) []string

// LookupMethodType resolves a method's request / response NamedTypeRef to its
// declaration plus the package prefix its bare mixins resolve against. A
// qualified ref (`shared.Holder`) is NOT in the consumer's bare-keyed
// pkg.Types, so it falls through the project resolver and the prefix is its
// home package; a local ref resolves in pkg.Types with an empty prefix.
//
// This is the single resolution every per-request / per-response codegen pass
// shares - the field resolver, default pre-fill, import collector, and
// response-header/cookie writers - so a qualified type is never silently
// dropped by one stage (an `undefined: pkg` import, a missing pre-fill, an
// unwritten response header) while a sibling stage emits it. Returns (nil, "")
// when unresolvable.
func LookupMethodType(ref *ast.NamedTypeRef, pkg *Package, r *Resolver) (*ast.TypeDecl, string) {
	if ref == nil || ref.Name == nil {
		return nil, ""
	}
	name := ref.Name.String()
	prefix := ""
	if parts := ref.Name.Parts; len(parts) == 2 {
		prefix = parts[0]
	}
	if td := r.LookupType(name); td != nil {
		return td, prefix
	}
	return nil, prefix
}

// ResolveFields flattens td (mixins expanded, generic args substituted -
// one body walk) and resolves every field. This is the single place the
// per-field facts are computed; stages read the result instead of
// re-deriving from the AST.
func ResolveFields(td *ast.TypeDecl, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	return ResolveFieldsWithPrefix(td, "", pkg, r, levelNames)
}

// ResolveFieldsWithPrefix is [ResolveFields] with the package-prefix
// context for bare mixins in td's body (see [FlattenFieldsIn]). A non-empty
// prefix is needed when td was reached through a qualified reference (a
// cross-package request type), so its bare nested mixins resolve in td's
// home package rather than being dropped.
func ResolveFieldsWithPrefix(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	flat := FlattenWithNames(td, prefix, pkg, r, map[string]bool{}, levelNames)
	out := make([]ResolvedField, 0, len(flat))
	for _, ff := range flat {
		rf := ResolveField(ff.Field, pkg, r.Project())
		// The dedup-resolved Go identifier from the declaring struct, so the
		// binder / default / response writer land on the same field the struct
		// declares (colliding siblings get the `_2` suffix everywhere).
		rf.Name = ff.Name
		out = append(out, rf)
	}
	return out
}

// RequestFields resolves m's request-type fields with method
// context applied: an un-decorated field auto-binds to @path (its name
// matches a `{param}` segment), to @query (a body-less verb has no body to
// decode into), or stays @body (a body verb). This is the single place the
// request auto-binding rule lives: every stage reads rf.Binding instead of
// re-deriving the path-segment + verb-default chain for itself.
func RequestFields(m *ast.Method, pkg *Package, r *Resolver, levelNames LevelNames) []ResolvedField {
	if m == nil || m.Request == nil {
		return nil
	}
	td, prefix := LookupMethodType(m.Request, pkg, r)
	if td == nil {
		return nil
	}
	// Full-route path variables (the owning service's @prefix vars + the method
	// path vars), so a field matching a @prefix variable auto-binds to @path -
	// the same shared rule the analyser's binding checks read, so codegen and
	// semantics can't disagree on where the field rides.
	pathNames := MethodRoutePathVars(m, pkg.Services)
	bodyVerb := wire.IsBodyVerb(m.Verb)
	fields := ResolveFieldsWithPrefix(td, prefix, pkg, r, levelNames)
	for i := range fields {
		rf := &fields[i]
		// Only an un-decorated field auto-binds. wire.ExplicitBinding maps both
		// `@body` and no-decorator to BindBody, so an explicit @body (raw
		// decorator non-empty) is left as body.
		if rf.Binding != wire.BindBody || wire.BindingKind(rf.Field.Decorators) != "" {
			continue
		}
		// The auto-binding rule lives in wire.RequestFieldBinding so the
		// analyser's binding checks and this resolver agree on where the field
		// rides.
		kind, auto := wire.RequestFieldBinding(rf.Field, pathNames, bodyVerb)
		rf.Binding = wire.BindingFromKind(kind)
		rf.AutoBound = auto
		rf.OnWireBody = rf.Binding == wire.BindBody
	}
	return fields
}
