package semantic

import (
	"maps"
	"slices"
	"strings"

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

// FlatField is a field of a type body or one its mixins promote. Its type is
// spelled as the flattening's view package spells it, with the generic
// arguments of the mixin that declares it substituted; Home is the package
// that declares it.
type FlatField struct {
	Field *ast.Field
	// Name is the selector that reaches the field from the flattened type:
	// the name [LevelNames] gave it in its own struct, behind the Go names of
	// the mixins embedding it when another member at its depth or above,
	// such as the mixin `Page` over a field `page`, has that name.
	Name string
	Home string
	// embedPath is the Go names of the mixins that embed the field, outermost first.
	embedPath []string
	// optionalParam reports a field declared `T?` for a type parameter T the
	// flattening binds: its Go value is a pointer to the argument's, which
	// Field's type spells as the argument made optional.
	optionalParam bool
	// paramTyped reports a field declared as a type parameter the flattening
	// binds, `T` or `T[]`: its type is spelled from the argument.
	paramTyped bool
}

// sliceBehindPointer reports whether ff's Go value is a pointer to a slice:
// ff is declared `T?` and its argument is an array.
func (ff FlatField) sliceBehindPointer() bool {
	return ff.optionalParam && ff.Field.Type.Array
}

// FlattenFields returns td's fields with its mixins expanded in body order,
// recursively, each mixin type once, and names running over each struct
// level's own body (nil leaves [FlatField.Name] empty). prefix is td's
// package when a qualified ref reached it, else "" for r's current package.
// Field types are spelled as r's current package spells them; a nil r
// expands no mixin.
func FlattenFields(td *ast.TypeDecl, prefix string, r *Resolver, names LevelNames) []FlatField {
	return flattenInstance(td, prefix, nil, r, names)
}

// flattenInstance is [FlattenFields] with td's type parameters bound to
// args, which r's current package spells.
func flattenInstance(td *ast.TypeDecl, prefix string, args []*ast.TypeRef, r *Resolver, names LevelNames) []FlatField {
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
	fields, _ := proj.flattenFields(view, home, td.Body, td.TypeParams, args, names)
	return fields
}

// flattenFields returns the fields of body, declared in package home with
// typeParams in scope and bound to args, and those its mixins promote,
// recursively in body order, each mixin type once. args and every field's
// type are spelled as package view spells them; nil args leave typeParams
// unbound. incomplete reports a mixin that names no type.
func (p *Project) flattenFields(view, home string, body []ast.TypeMember, typeParams []string, args []*ast.TypeRef, names LevelNames) (fields []FlatField, incomplete bool) {
	w := &fieldWalk{proj: p, view: view, names: names, expanded: map[string]bool{}, embedDepths: map[string][]int{}}
	fields = w.level(home, body, typeParams, SubstMap(typeParams, args), nil)
	w.nameShadowedByPath(fields)
	return fields, w.incomplete
}

// fieldWalk is the state of one [Project.flattenFields].
type fieldWalk struct {
	proj       *Project
	view       string
	names      LevelNames
	expanded   map[string]bool // canonical `pkg.Name` of each mixin type expanded
	incomplete bool
	// embedDepths maps the Go name of each mixin embedded to the depths it
	// sits at, the flattened type's own body being depth 0.
	embedDepths map[string][]int
}

// levelName returns the name [LevelNames] gave ff in its own struct:
// [FlatField.Name] without the embed path.
func (ff FlatField) levelName() string {
	return ff.Name[strings.LastIndexByte(ff.Name, '.')+1:]
}

// nameShadowedByPath gives each promoted field that another member at its
// depth or above shares a name with its embed path as [FlatField.Name].
func (w *fieldWalk) nameShadowedByPath(fields []FlatField) {
	if w.names == nil {
		return
	}
	depths := maps.Clone(w.embedDepths)
	for _, ff := range fields {
		depths[ff.Name] = append(depths[ff.Name], len(ff.embedPath))
	}
	for i := range fields {
		ff := &fields[i]
		depth := len(ff.embedPath)
		if depth == 0 {
			continue
		}
		above := 0 // the members named ff.Name at its depth or above, itself included
		for _, d := range depths[ff.Name] {
			if d <= depth {
				above++
			}
		}
		if above > 1 {
			ff.Name = strings.Join(append(slices.Clone(ff.embedPath), ff.Name), ".")
		}
	}
}

// level returns the fields of one struct level declared in package home,
// its typeParams bound by subst; embedPath is the Go names of the mixins
// that embed the level, outermost first.
func (w *fieldWalk) level(home string, body []ast.TypeMember, typeParams []string, subst map[string]*ast.TypeRef, embedPath []string) []FlatField {
	var names []string
	if w.names != nil {
		names = w.names(body)
	}
	var out []FlatField
	i := 0
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			ff := FlatField{Field: v, Home: home, embedPath: embedPath,
				optionalParam: optionalParam(v.Type, subst), paramTyped: boundParam(v.Type, subst) != nil}
			if i < len(names) {
				ff.Name = names[i]
			}
			if t := w.spell(v.Type, home, typeParams, subst); t != v.Type {
				fc := *v
				fc.Type = t
				ff.Field = &fc
			}
			out = append(out, ff)
			i++
		case *ast.Mixin:
			out = append(out, w.mixin(home, v, typeParams, subst, embedPath)...)
		}
	}
	return out
}

// optionalParam reports whether t is `T?` for a type parameter T that subst
// binds.
func optionalParam(t *ast.TypeRef, subst map[string]*ast.TypeRef) bool {
	return t != nil && t.Optional && !t.Array && boundParam(t, subst) != nil
}

// boundParam returns the argument subst binds to the type parameter t names,
// whatever t's suffixes; nil when t names none.
func boundParam(t *ast.TypeRef, subst map[string]*ast.TypeRef) *ast.TypeRef {
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Name.Parts) != 1 {
		return nil
	}
	return subst[t.Named.Name.Parts[0]]
}

// spell returns t, written in package home with typeParams in scope and
// bound by subst, as the walk's view package spells it.
func (w *fieldWalk) spell(t *ast.TypeRef, home string, typeParams []string, subst map[string]*ast.TypeRef) *ast.TypeRef {
	t = w.proj.requalify(t, home, w.view, typeParams)
	if len(subst) == 0 {
		return t
	}
	return SubstituteTypeRef(t, subst)
}

// mixin returns the fields mx, written in package home with typeParams in
// scope and bound by subst and embedded behind embedPath, promotes:
// `Page<Item>` promotes `items T[]` as `items Item[]`. Its arguments bind the
// mixin's own level alone.
func (w *fieldWalk) mixin(home string, mx *ast.Mixin, typeParams []string, subst map[string]*ast.TypeRef, embedPath []string) []FlatField {
	if mx.Ref == nil {
		return nil
	}
	pkg, sym := w.proj.resolve(home, mx.Ref.Name)
	if pkg == nil || pkg.Types[sym] == nil {
		w.incomplete = true
		return nil
	}
	embed := goEmbedName(mx.Ref.Name)
	w.embedDepths[embed] = append(w.embedDepths[embed], len(embedPath))
	key := pkg.Name + "." + sym
	if w.expanded[key] {
		return nil
	}
	w.expanded[key] = true
	td := pkg.Types[sym]
	args := make([]*ast.TypeRef, len(mx.Ref.Args))
	for i, a := range mx.Ref.Args {
		args[i] = w.spell(a, home, typeParams, subst)
	}
	return w.level(pkg.Name, td.Body, td.TypeParams, SubstMap(td.TypeParams, args), append(slices.Clip(embedPath), embed))
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

// instanceFields returns the fields of the type ref names, mixins included
// and its generic arguments substituted, spelled as view, the package
// declaring the type, spells them; ok is false when ref names no type.
func (a *analyzer) instanceFields(ref *ast.NamedTypeRef) (view string, fields []FlatField, ok bool) {
	if ref == nil {
		return "", nil, false
	}
	pkg, sym := a.proj.resolve(a.pkg.Name, ref.Name)
	if pkg == nil || pkg.Types[sym] == nil {
		return "", nil, false
	}
	td := pkg.Types[sym]
	args := make([]*ast.TypeRef, len(ref.Args))
	for i, arg := range ref.Args {
		args[i] = a.proj.requalify(arg, a.pkg.Name, pkg.Name, nil)
	}
	fields, _ = a.proj.flattenFields(pkg.Name, pkg.Name, td.Body, td.TypeParams, args, nil)
	return pkg.Name, fields, true
}
