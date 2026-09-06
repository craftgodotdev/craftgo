// The flattened field view of a type: mixins expanded and generic
// arguments substituted, with each field's dedup-resolved Go name.
package codegen

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// substMap pairs a generic decl's type parameters with the concrete
// arguments of one instantiation (`T` → `Item`). Extra params beyond the
// supplied args are left unmapped. The OpenAPI schema instantiation, the
// response-field substitution, and the mixin field flatten all build this
// same map, so they share one definition.
func substMap(typeParams []string, args []*ast.TypeRef) map[string]*ast.TypeRef {
	subst := make(map[string]*ast.TypeRef, len(typeParams))
	for i, p := range typeParams {
		if i < len(args) {
			subst[p] = args[i]
		}
	}
	return subst
}

// substituteTypeRef walks t and swaps every NamedTypeRef whose Name is
// a known type-param key with the matching concrete TypeRef. Array and
// Optional suffixes from the original survive; the substituted ref's
// own suffixes are merged in too (so `T?` substituted with `Book[]`
// correctly produces `Book[]?`).
func substituteTypeRef(t *ast.TypeRef, subst map[string]*ast.TypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	if t.Map != nil {
		return &ast.TypeRef{
			Pos: t.Pos,
			Map: &ast.MapType{
				Pos:   t.Map.Pos,
				Key:   substituteTypeRef(t.Map.Key, subst),
				Value: substituteTypeRef(t.Map.Value, subst),
			},
			Array:      t.Array,
			ArrayDepth: t.ArrayDepth,
			Optional:   t.Optional,
		}
	}
	if t.Named != nil {
		if rep, ok := subst[t.Named.Name.String()]; ok {
			out := *rep
			if t.Array {
				out.Array = true
				// Add the outer's array dim count on top of any
				// the substituted ref carried (e.g. `T?` →
				// `Book[]` becomes `Book[]?` with depth=1).
				if t.ArrayDepth > 0 {
					out.ArrayDepth += t.ArrayDepth
				} else if out.ArrayDepth == 0 {
					out.ArrayDepth = 1
				}
			}
			if t.Optional {
				out.Optional = true
			}
			return &out
		}
		// The Named ref itself is not a type-param, but its generic
		// args might be: `kids: Tree<T>[]` inside `type Tree<T>` has
		// `Tree` (not a param) plus arg `T` (a param). Substitute
		// inside the args so the synthesized instance carries the
		// concrete arg, not the still-bound param. Without this the
		// post-substitution body would register the parametric
		// `Tree<T>` again at every recursive site, polluting the
		// component map with phantom `TreeOfT` entries.
		if len(t.Named.Args) > 0 {
			args := make([]*ast.TypeRef, len(t.Named.Args))
			subbed := false
			for i, a := range t.Named.Args {
				args[i] = substituteTypeRef(a, subst)
				if args[i] != a {
					subbed = true
				}
			}
			if subbed {
				cp := *t
				named := *t.Named
				named.Args = args
				cp.Named = &named
				return &cp
			}
		}
	}
	return t
}

// flattenFields returns td's fields with embedded mixins expanded in
// declaration order: every `Mixin` member contributes the fields of the
// type it names (recursively), the same fields the JSON body schema
// (allOf $ref) and the validator (mixinValidateCall) already pull in. The
// wire-binding, OpenAPI-parameter, default pre-fill, and body-decode
// passes call this so a field a request inherits through a mixin is bound,
// documented, defaulted, and decoded - not silently dropped while the
// validator still enforces it. `r` may be nil (the OpenAPI pass runs on
// the merged single package, where pkg.Types already holds every type);
// `seen` breaks mixin cycles.
func flattenFields(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver, seen map[string]bool) []*ast.Field {
	return flattenFieldsIn(td, "", pkg, r, seen)
}

// flattenFieldsIn is [flattenFields] with a package-prefix context.
// `prefix` is the package qualifier for BARE mixin names in td's body:
// "" for the package being generated, or a sibling package name when td
// was itself reached through a cross-package mixin. Without it a bare
// mixin nested inside `shared.XMid` (e.g. `XDeep`, declared in `shared`)
// is looked up against the current package and silently dropped - so its
// fields never bind, default, or validate, while OpenAPI (built from a
// flattened merged package) still advertises them. The prefix qualifies
// the bare name (`shared.XDeep`) so the resolver finds it.
func flattenFieldsIn(td *ast.TypeDecl, prefix string, pkg *semantic.Package, r *ProjectResolver, seen map[string]bool) []*ast.Field {
	flat := flattenFieldsWithNames(td, prefix, pkg, r, seen)
	out := make([]*ast.Field, len(flat))
	for i, ff := range flat {
		out[i] = ff.Field
	}
	return out
}

// flatField is a flattened request/response field paired with the Go
// identifier it lands on. GoName is deduped within the field's DECLARING
// struct (the type whose body literally lists it), so it matches what the
// struct renderer emits - colliding siblings (`userId` / `user_id`) get the
// `_2`/`_3` suffix in EVERY consumer (wire binder, default pre-fill, response
// writer), not just the struct. A field promoted through a mixin keeps the
// name from its own declaring struct, since the request embeds that mixin
// (Go field promotion) rather than inlining its fields.
type flatField struct {
	Field  *ast.Field
	GoName string
}

// flattenFieldsWithNames is [flattenFieldsIn] carrying each field's
// dedup-resolved Go identifier. The dedup runs PER recursion level (over the
// declaring type's direct fields), mirroring the struct renderer, so the
// suffix a colliding field gets is identical to its struct field - the single
// source of the Go field identity the whole pipeline reads.
func flattenFieldsWithNames(td *ast.TypeDecl, prefix string, pkg *semantic.Package, r *ProjectResolver, seen map[string]bool) []flatField {
	if td == nil {
		return nil
	}
	// Dedup this level's direct fields exactly as the struct renderer does, so
	// a promoted field carries the name it has in its own struct.
	levelNames := resolvedGoFieldNames(td.Body)
	var out []flatField
	fieldIdx := 0
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			// A field promoted from a foreign package (prefix != "") names
			// its type bare in its home package; re-qualify so the
			// consumer's resolver (binder cast, default pre-fill, import
			// collector) finds it as `prefix.Name`. No-op at the top level
			// and for the r=nil (merged-package OpenAPI) path.
			out = append(out, flatField{Field: requalifyFieldType(v, prefix, r), GoName: levelNames[fieldIdx]})
			fieldIdx++
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			// Resolve the mixin in the package it lives in: a qualified
			// ref names that package; a bare ref inherits the enclosing
			// prefix (the package td itself came from).
			parts := v.Ref.Name.Parts
			key := v.Ref.Name.String()
			childPrefix := prefix
			if len(parts) == 2 {
				childPrefix = parts[0]
			} else if prefix != "" {
				key = prefix + "." + key
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			mt := r.LookupType(key)
			sub := flattenFieldsWithNames(mt, childPrefix, pkg, r, seen)
			// A generic mixin (`Page<Item>`) promotes fields typed in the
			// type-parameter (`items T[]`). Substitute the concrete arguments
			// so every consumer - wire binder, OpenAPI params/body, default
			// pre-fill - sees `items Item[]`, not the bare `T`.
			if mt != nil && len(v.Ref.Args) > 0 && len(mt.TypeParams) > 0 {
				subst := substMap(mt.TypeParams, v.Ref.Args)
				for i := range sub {
					fc := *sub[i].Field
					fc.Type = substituteTypeRef(sub[i].Field.Type, subst)
					sub[i].Field = &fc
				}
			}
			out = append(out, sub...)
		}
	}
	return out
}

// requestFields is the mixin-aware field list of a request / response
// type: [flattenFields] with a fresh cycle-guard.
func requestFields(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver) []*ast.Field {
	return flattenFields(td, pkg, r, map[string]bool{})
}

// requalifyFieldType returns f with its type re-qualified into package
// `prefix` (see [requalifyTypeRef]), cloning only when a rewrite is needed.
func requalifyFieldType(f *ast.Field, prefix string, r *ProjectResolver) *ast.Field {
	if f == nil || prefix == "" || r == nil || f.Type == nil {
		return f
	}
	nt := requalifyTypeRef(f.Type, prefix, r)
	if nt == f.Type {
		return f
	}
	fc := *f
	fc.Type = nt
	return &fc
}

// requalifyTypeRef rewrites every BARE named ref in t that names a
// type / scalar / enum declared in package `prefix` into the qualified
// form `prefix.Name`, recursing through arrays, map keys/values, and
// generic args. A bare name that does NOT resolve in `prefix` (a builtin
// like `string`/`int`, or a generic type-parameter) is left as-is. Used
// when a field is promoted into another package through a cross-package
// mixin: its type, written bare in its home package, must be qualified so
// the consumer's resolver finds the scalar / enum / type.
func requalifyTypeRef(t *ast.TypeRef, prefix string, r *ProjectResolver) *ast.TypeRef {
	if t == nil || prefix == "" || r == nil {
		return t
	}
	if t.Map != nil {
		nk := requalifyTypeRef(t.Map.Key, prefix, r)
		nv := requalifyTypeRef(t.Map.Value, prefix, r)
		if nk == t.Map.Key && nv == t.Map.Value {
			return t
		}
		clone := *t
		mc := *t.Map
		mc.Key, mc.Value = nk, nv
		clone.Map = &mc
		return &clone
	}
	if t.Named == nil || t.Named.Name == nil {
		return t
	}
	var newArgs []*ast.TypeRef
	argsChanged := false
	if len(t.Named.Args) > 0 {
		newArgs = make([]*ast.TypeRef, len(t.Named.Args))
		for i, a := range t.Named.Args {
			newArgs[i] = requalifyTypeRef(a, prefix, r)
			if newArgs[i] != a {
				argsChanged = true
			}
		}
	}
	qualify := false
	if len(t.Named.Name.Parts) == 1 {
		q := prefix + "." + t.Named.Name.Parts[0]
		if r.LookupType(q) != nil || r.LookupScalar(q) != nil || r.LookupEnum(q) != nil {
			qualify = true
		}
	}
	if !qualify && !argsChanged {
		return t
	}
	clone := *t
	named := *t.Named
	if argsChanged {
		named.Args = newArgs
	}
	if qualify {
		nm := *t.Named.Name
		nm.Parts = []string{prefix, t.Named.Name.Parts[0]}
		named.Name = &nm
	}
	clone.Named = &named
	return &clone
}
