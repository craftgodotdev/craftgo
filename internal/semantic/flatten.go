package semantic

import (
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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
// through r, so a nil r expands none.
func FlattenFields(td *ast.TypeDecl, pkg *Package, r *Resolver) []*ast.Field {
	return FlattenFieldsIn(td, "", pkg, r)
}

// FlattenFieldsIn is [FlattenFields] for a td reached through a qualified
// ref: prefix is td's package, where its bare mixins resolve.
func FlattenFieldsIn(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver) []*ast.Field {
	flat := FlattenWithNames(td, prefix, pkg, r, nil)
	out := make([]*ast.Field, len(flat))
	for i, ff := range flat {
		out[i] = ff.Field
	}
	return out
}

// FlatField is a field of a type body or one its mixins promote, and the
// name [LevelNames] gave it in its own struct. Its type is spelled as the
// flattening's view package spells it, a promoted field's with the mixin's
// generic arguments substituted; Home is the package that declares it.
type FlatField struct {
	Field *ast.Field
	Name  string
	Home  string
}

// FlattenWithNames is [FlattenFieldsIn] that also names each field, running
// levelNames over each struct level's own body. Field types are spelled as
// r's current package spells them.
func FlattenWithNames(td *ast.TypeDecl, prefix string, pkg *Package, r *Resolver, levelNames LevelNames) []FlatField {
	if td == nil {
		return nil
	}
	var proj *Project
	view := ""
	if r != nil {
		proj, view = r.proj, r.current
	}
	home := prefix
	if home == "" {
		home = view
	}
	fields, _ := proj.flattenFields(view, home, td.Body, td.TypeParams, levelNames)
	return fields
}

// flattenFields returns the fields of body, declared in package home with
// typeParams in scope, and those its mixins promote, recursively in body
// order, each mixin type once. Every field's type is spelled as package view
// spells it. incomplete reports a mixin that names no type.
func (p *Project) flattenFields(view, home string, body []ast.TypeMember, typeParams []string, names LevelNames) (fields []FlatField, incomplete bool) {
	w := &fieldWalk{proj: p, view: view, names: names, expanded: map[string]bool{}}
	fields = w.level(home, body, typeParams)
	return fields, w.incomplete
}

// fieldWalk is the state of one [Project.flattenFields].
type fieldWalk struct {
	proj       *Project
	view       string
	names      LevelNames
	expanded   map[string]bool // canonical `pkg.Name` of each mixin type expanded
	incomplete bool
}

// level returns the fields of one struct level declared in package home.
func (w *fieldWalk) level(home string, body []ast.TypeMember, typeParams []string) []FlatField {
	var names []string
	if w.names != nil {
		names = w.names(body)
	}
	var out []FlatField
	i := 0
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			ff := FlatField{Field: v, Home: home}
			if i < len(names) {
				ff.Name = names[i]
			}
			if t := w.proj.requalify(v.Type, home, w.view, typeParams); t != v.Type {
				fc := *v
				fc.Type = t
				ff.Field = &fc
			}
			out = append(out, ff)
			i++
		case *ast.Mixin:
			out = append(out, w.mixin(home, v, typeParams)...)
		}
	}
	return out
}

// mixin returns the fields mx, written in package home, promotes: `Page<Item>`
// promotes `items T[]` as `items Item[]`.
func (w *fieldWalk) mixin(home string, mx *ast.Mixin, typeParams []string) []FlatField {
	if mx.Ref == nil {
		return nil
	}
	pkg, sym := w.proj.resolve(home, mx.Ref.Name)
	if pkg == nil || pkg.Types[sym] == nil {
		w.incomplete = true
		return nil
	}
	key := pkg.Name + "." + sym
	if w.expanded[key] {
		return nil
	}
	w.expanded[key] = true
	td := pkg.Types[sym]
	fields := w.level(pkg.Name, td.Body, td.TypeParams)
	if len(mx.Ref.Args) == 0 || len(td.TypeParams) == 0 {
		return fields
	}
	args := make([]*ast.TypeRef, len(mx.Ref.Args))
	for i, a := range mx.Ref.Args {
		args[i] = w.proj.requalify(a, home, w.view, typeParams)
	}
	subst := SubstMap(td.TypeParams, args)
	for i := range fields {
		fc := *fields[i].Field
		fc.Type = SubstituteTypeRef(fc.Type, subst)
		fields[i].Field = &fc
	}
	return fields
}

// requalify spells t, written in package home with typeParams in scope, as
// package view spells it: a bare name home declares becomes `home.Name`,
// through map keys and values and generic arguments. t is cloned only when
// a name changes.
func (p *Project) requalify(t *ast.TypeRef, home, view string, typeParams []string) *ast.TypeRef {
	if t == nil || home == view || p == nil || p.Packages[home] == nil {
		return t
	}
	if t.Map != nil {
		key := p.requalify(t.Map.Key, home, view, typeParams)
		value := p.requalify(t.Map.Value, home, view, typeParams)
		if key == t.Map.Key && value == t.Map.Value {
			return t
		}
		clone := *t
		mc := *t.Map
		mc.Key, mc.Value = key, value
		clone.Map = &mc
		return &clone
	}
	if t.Named == nil || t.Named.Name == nil {
		return t
	}
	args := make([]*ast.TypeRef, len(t.Named.Args))
	argsChanged := false
	for i, a := range t.Named.Args {
		args[i] = p.requalify(a, home, view, typeParams)
		if args[i] != a {
			argsChanged = true
		}
	}
	parts := t.Named.Name.Parts
	qualify := len(parts) == 1 && !slices.Contains(typeParams, parts[0]) &&
		p.Packages[home].Decl(parts[0], TypeRefDecls) != nil
	if !qualify && !argsChanged {
		return t
	}
	clone := *t
	named := *t.Named
	if argsChanged {
		named.Args = args
	}
	if qualify {
		name := *t.Named.Name
		name.Parts = []string{home, parts[0]}
		named.Name = &name
	}
	clone.Named = &named
	return &clone
}
