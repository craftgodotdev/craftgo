package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// SubstMap pairs a generic decl's type parameters with the arguments of one
// instantiation (`T` → `Item`); params beyond the supplied args stay unmapped.
func SubstMap(typeParams []string, args []*ast.TypeRef) map[string]*ast.TypeRef {
	subst := make(map[string]*ast.TypeRef, len(typeParams))
	for i, p := range typeParams {
		if i < len(args) {
			subst[p] = args[i]
		}
	}
	return subst
}

// SubstituteTypeRef replaces every type parameter in t with its argument
// from subst, adding t's own suffixes: `T?` with `Book[]` gives `Book[]?`.
func SubstituteTypeRef(t *ast.TypeRef, subst map[string]*ast.TypeRef) *ast.TypeRef {
	if t == nil {
		return nil
	}
	if t.Map != nil {
		return &ast.TypeRef{
			Pos: t.Pos,
			Map: &ast.MapType{
				Pos:   t.Map.Pos,
				Key:   SubstituteTypeRef(t.Map.Key, subst),
				Value: SubstituteTypeRef(t.Map.Value, subst),
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
				// t's array depth adds to the argument's own.
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
		// A non-parameter ref may still carry parameters as arguments:
		// `kids Tree<T>[]` inside `type Tree<T>`.
		if len(t.Named.Args) > 0 {
			args := make([]*ast.TypeRef, len(t.Named.Args))
			subbed := false
			for i, a := range t.Named.Args {
				args[i] = SubstituteTypeRef(a, subst)
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

// FlattenFields returns td's fields with its mixins expanded in body order,
// recursively, and their generic arguments substituted. Mixins resolve
// through r, so a nil r expands none; seen breaks mixin cycles.
func FlattenFields(td *ast.TypeDecl, pkg *Package, r *Resolver, seen map[string]bool) []*ast.Field {
	return FlattenFieldsIn(td, "", pkg, r, seen)
}

// FlattenFieldsIn is [FlattenFields] for a td reached through a qualified
// ref: prefix is td's package, where its bare mixins resolve.
func FlattenFieldsIn(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver, seen map[string]bool) []*ast.Field {
	flat := FlattenWithNames(td, prefix, pkg, r, seen, nil)
	out := make([]*ast.Field, len(flat))
	for i, ff := range flat {
		out[i] = ff.Field
	}
	return out
}

// RequalifyFieldType returns f with its type re-qualified into package
// `prefix` (see [RequalifyTypeRef]), cloning only when a rewrite is needed.
func RequalifyFieldType(f *ast.Field, prefix string, r *Resolver) *ast.Field {
	if f == nil || prefix == "" || r == nil || f.Type == nil {
		return f
	}
	nt := RequalifyTypeRef(f.Type, prefix, r)
	if nt == f.Type {
		return f
	}
	fc := *f
	fc.Type = nt
	return &fc
}

// RequalifyTypeRef qualifies every bare name in t that package prefix
// declares as a type, scalar or enum, through map keys, values and generic
// arguments; other names stay bare. t is cloned only when a name changes.
func RequalifyTypeRef(t *ast.TypeRef, prefix string, r *Resolver) *ast.TypeRef {
	if t == nil || prefix == "" || r == nil {
		return t
	}
	if t.Map != nil {
		nk := RequalifyTypeRef(t.Map.Key, prefix, r)
		nv := RequalifyTypeRef(t.Map.Value, prefix, r)
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
			newArgs[i] = RequalifyTypeRef(a, prefix, r)
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

// FlatField is a flattened field and the name [LevelNames] gave it in its
// own struct.
type FlatField struct {
	Field *ast.Field
	Name  string
}

// nameAt returns the i-th entry of names, or "" past its end.
func nameAt(names []string, i int) string {
	if i < len(names) {
		return names[i]
	}
	return ""
}

// FlattenWithNames is [FlattenFieldsIn] that also names each field, running
// levelNames over each struct level's own body.
func FlattenWithNames(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver, seen map[string]bool, levelNames func([]ast.TypeMember) []string) []FlatField {
	if td == nil {
		return nil
	}
	var names []string
	if levelNames != nil {
		names = levelNames(td.Body)
	}
	var out []FlatField
	fieldIdx := 0
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			// A field declared in package prefix gets its bare names qualified.
			out = append(out, FlatField{Field: RequalifyFieldType(v, prefix, r), Name: nameAt(names, fieldIdx)})
			fieldIdx++
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			// A bare mixin resolves in td's own package, prefix.
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
			sub := FlattenWithNames(mt, childPrefix, pkg, r, seen, levelNames)
			// `Page<Item>` promotes `items T[]` as `items Item[]`.
			if mt != nil && len(v.Ref.Args) > 0 && len(mt.TypeParams) > 0 {
				subst := SubstMap(mt.TypeParams, v.Ref.Args)
				for i := range sub {
					fc := *sub[i].Field
					fc.Type = SubstituteTypeRef(sub[i].Field.Type, subst)
					sub[i].Field = &fc
				}
			}
			out = append(out, sub...)
		}
	}
	return out
}
