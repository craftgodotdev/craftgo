package golang

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// bodyFile holds a transport segment's wire structs; no method file takes a name with a dot
// before `.go`.
const bodyFile = "bodies.gen.go"

// bodyNames are the names the wire structs' copy methods bind.
var bodyNames = []string{"server", "wire", "w", "dst", "_p", "_v", "_d"}

// typeEnv is the scope a type reference is written in: the DSL package declaring it and the type
// parameters of the declaration whose body holds it, bound to their arguments.
type typeEnv struct {
	home   string
	params map[string]typeArg
}

// typeArg is a type argument and the scope it is written in.
type typeArg struct {
	ref *ast.TypeRef
	env *typeEnv
}

// param returns the argument n names when n is one of env's type parameters.
func (env *typeEnv) param(n *ast.NamedTypeRef) (typeArg, bool) {
	if n == nil || n.Name == nil || len(n.Name.Parts) != 1 {
		return typeArg{}, false
	}
	a, ok := env.params[n.Name.Parts[0]]
	return a, ok
}

// bodyPlan holds the wire structs one transport segment's handlers decode JSON request bodies
// into. A wire struct mirrors one instance of a struct type a body reaches, its mixins embedded as
// theirs: a member is a pointer, nil while absent or null, so a required one is told apart from a
// zero value; the struct's `to` method checks the required members and copies the body into the
// request type, filling the @default of each member absent or null on the way.
type bodyPlan struct {
	proj *semantic.Project
	view string
	r    *projectResolver
	// imports spells the types of bodyFile; keys spells instance keys apart from it, so a type
	// only weighed is not imported.
	imports, keys *importSet
	byKey         map[string]*wireStruct
	order         []*wireStruct
	// tops maps each body-decoding method to the instance its request decodes into.
	tops map[*ast.Method]*wireStruct
}

// wireStruct is one instance of a struct type a request body reaches.
type wireStruct struct {
	ref   *ast.NamedTypeRef
	env   *typeEnv // the scope ref is written in
	home  string
	inner *typeEnv // the scope of the type's body
	// skip names the members the method's route fills, on the request type or a mixin it holds.
	skip  map[string]bool
	items []wireItem
	// checks reports a required member of its own; defaults a defaulted one.
	checks, defaults bool
	// nonTrivial reports that a value of the type decodes through a wire struct: it, or a value
	// or mixin below it, checks a member or fills a default.
	nonTrivial bool
	emitted    bool
	name       string
}

// wireItem is a member of a wire struct in body order: a field or a mixin.
type wireItem struct {
	field *wireMember
	mixin *wireMixin
}

// wireMixin is a mixin of a wire struct: its instance and the Go name the request type embeds it as.
type wireMixin struct {
	s     *wireStruct
	embed string
}

// wireMember is one JSON member a struct's own field carries.
type wireMember struct {
	goName   string
	json     string
	presence wire.JSONPresence
	// value is the field's type without its `?`.
	value *ast.TypeRef
	// pubPtr reports that the request type holds the value behind a pointer.
	pubPtr bool
	raw    bool
	// skipped reports a member the route fills, which the copy leaves alone.
	skipped bool
	// def is the @default argument, nil without one the copy can fill.
	def      *ast.DecoratorArg
	children []*wireStruct
}

// newBodyPlan returns the plan of the segment whose DSL package r resolves in, its file importing
// the package's types under localAlias from typesPath.
func newBodyPlan(r *projectResolver, view, typesPath string) *bodyPlan {
	home := goImport{Alias: localAlias, Path: typesPath}
	return &bodyPlan{
		proj:    r.Project(),
		view:    view,
		r:       r,
		imports: newImportSet(r.Module, r, home, bodyNames),
		keys:    newImportSet(r.Module, r, home, bodyNames),
		byKey:   map[string]*wireStruct{},
		tops:    map[*ast.Method]*wireStruct{},
	}
}

// add weighs m's request body; fields are its request fields, which name the members its route
// fills.
func (p *bodyPlan) add(m *ast.Method, fields []resolvedField) {
	skip := map[string]bool{}
	for _, rf := range fields {
		if rf.AutoBound && rf.Binding == wire.BindPath {
			skip[rf.Field.Name] = true
		}
	}
	if s := p.instance(m.Request, &typeEnv{home: p.view}, skip); s != nil {
		p.tops[m] = s
	}
}

// structDecl returns the struct type n names in env and the package declaring it, or nil.
func (p *bodyPlan) structDecl(n *ast.NamedTypeRef, env *typeEnv) (string, *ast.TypeDecl) {
	if n == nil || n.Name == nil || p.proj == nil {
		return "", nil
	}
	if _, ok := env.param(n); ok {
		return "", nil
	}
	home, sym := env.home, n.Name.String()
	if parts := n.Name.Parts; len(parts) == 2 {
		home, sym = parts[0], parts[1]
	}
	pkg := p.proj.Packages[home]
	if pkg == nil || pkg.Types[sym] == nil {
		return "", nil
	}
	return home, pkg.Types[sym]
}

// instance returns the wire struct of the struct instance ref, written in env, names, weighing it
// on first sight; nil when ref names no struct type.
func (p *bodyPlan) instance(ref *ast.NamedTypeRef, env *typeEnv, skip map[string]bool) *wireStruct {
	home, td := p.structDecl(ref, env)
	if td == nil {
		return nil
	}
	inner := &typeEnv{home: home, params: map[string]typeArg{}}
	for i, tp := range td.TypeParams {
		if i < len(ref.Args) {
			inner.params[tp] = typeArg{ref: ref.Args[i], env: env}
		}
	}
	skip = p.skipWithin(td, inner, skip, map[*ast.TypeDecl]bool{})
	key := p.key(ref, env, skip)
	if s, ok := p.byKey[key]; ok {
		return s
	}
	s := &wireStruct{ref: ref, env: env, home: home, inner: inner, skip: skip}
	p.byKey[key] = s
	p.order = append(p.order, s)
	names := resolvedGoFieldNames(td.Body)
	i := 0
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			goName := names[i]
			i++
			if mem := p.member(s, v, goName); mem != nil {
				s.items = append(s.items, wireItem{field: mem})
			}
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			if child := p.instance(v.Ref, inner, skip); child != nil {
				s.items = append(s.items, wireItem{mixin: &wireMixin{s: child, embed: v.Ref.Name.Parts[len(v.Ref.Name.Parts)-1]}})
			}
		}
	}
	return s
}

// skipWithin returns the names of skip a field of td's body, or of a mixin below it, declares;
// nil when none does.
func (p *bodyPlan) skipWithin(td *ast.TypeDecl, env *typeEnv, skip map[string]bool, seen map[*ast.TypeDecl]bool) map[string]bool {
	if len(skip) == 0 || seen[td] {
		return nil
	}
	seen[td] = true
	var out map[string]bool
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			if skip[v.Name] {
				if out == nil {
					out = map[string]bool{}
				}
				out[v.Name] = true
			}
		case *ast.Mixin:
			home, mtd := p.structDecl(v.Ref, env)
			if mtd == nil {
				continue
			}
			for name := range p.skipWithin(mtd, &typeEnv{home: home}, skip, seen) {
				if out == nil {
					out = map[string]bool{}
				}
				out[name] = true
			}
		}
	}
	return out
}

// key identifies the instance ref names in env, with the members skip leaves to the route.
func (p *bodyPlan) key(ref *ast.NamedTypeRef, env *typeEnv, skip map[string]bool) string {
	k := goNamedType(ref, semantic.NewResolver(p.proj, env.home), p.declared(p.keys, env))
	if len(skip) > 0 {
		names := make([]string, 0, len(skip))
		for n := range skip {
			names = append(names, n)
		}
		slices.Sort(names)
		k += " skip " + strings.Join(names, ",")
	}
	return k
}

// member returns the JSON member f, a field of s Go-named goName, carries, or nil for a field off
// the body.
func (p *bodyPlan) member(s *wireStruct, f *ast.Field, goName string) *wireMember {
	jsonName, presence := wire.JSONShape(f)
	if presence == wire.JSONAbsent || f.Type == nil {
		return nil
	}
	rf := semantic.ResolveField(f, p.proj.Packages[s.home], p.proj)
	value := *f.Type
	value.Optional = false
	_, explicit := wire.BindingKind(f.Decorators)
	m := &wireMember{
		goName:   goName,
		json:     jsonName,
		presence: presence,
		value:    &value,
		pubPtr:   rf.GoPointer() && !rf.SpellsPointer(),
		raw:      rf.Category == semantic.CatRawBytes,
		skipped:  s.skip[f.Name] && !explicit,
	}
	arg, paramTyped := s.inner.param(f.Type.Named)
	if paramTyped && f.Type.ArrayDepth == 0 && f.Type.Map == nil {
		// A raw argument is told apart where it is written: T itself resolves to no category.
		m.raw = semantic.NewResolver(p.proj, arg.env.home).ResolveTypeRef(arg.ref).Category == semantic.CatRawBytes
	}
	if arg := firstArg(f.Decorators, "default"); arg != nil && rf.HasDefValue && f.Type.Map == nil && !paramTyped &&
		renderDefault(p.viewRef(&value, s.inner), arg.Value, p.r, p.keys.scratch()) != "" {
		m.def = arg
	}
	p.walkValue(m.value, s.inner, func(c *wireStruct) { m.children = append(m.children, c) })
	return m
}

// walkValue calls fn with the instance of each struct a value of type t, written in env, holds
// directly or in an element or map value.
func (p *bodyPlan) walkValue(t *ast.TypeRef, env *typeEnv, fn func(*wireStruct)) {
	if t == nil {
		return
	}
	if t.Map != nil {
		p.walkValue(t.Map.Value, env, fn)
		return
	}
	if a, ok := env.param(t.Named); ok {
		p.walkValue(a.ref, a.env, fn)
		return
	}
	if s := p.instance(t.Named, env, nil); s != nil {
		fn(s)
	}
}

// finish weighs which instances decode through a wire struct and names those the planned
// requests reach.
func (p *bodyPlan) finish() {
	for _, s := range p.order {
		for _, it := range s.items {
			if m := it.field; m != nil && !m.skipped {
				switch {
				case m.def != nil:
					s.defaults = true
				case m.presence != wire.JSONOptional:
					s.checks = true
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range p.order {
			if !s.nonTrivial && (s.checks || s.defaults || slices.ContainsFunc(s.children(), func(c *wireStruct) bool { return c.nonTrivial })) {
				s.nonTrivial, changed = true, true
			}
		}
	}
	for m, top := range p.tops {
		if top.nonTrivial {
			top.emit()
		} else {
			delete(p.tops, m)
		}
	}
	taken := map[string]bool{}
	for _, s := range p.order {
		if !s.emitted {
			continue
		}
		base := idents.FileName(strings.Join(p.nameWords(s.ref, s.env), ""), idents.FileCaseCamel) + "Wire"
		s.name = base
		for i := 2; taken[s.name]; i++ {
			s.name = base + strconv.Itoa(i)
		}
		taken[s.name] = true
	}
}

// children returns the instances s's members hold and its mixins.
func (s *wireStruct) children() []*wireStruct {
	var out []*wireStruct
	for _, it := range s.items {
		if it.field != nil {
			out = append(out, it.field.children...)
		} else {
			out = append(out, it.mixin.s)
		}
	}
	return out
}

// emit marks s and what its wire struct names: each mixin carrying a member, and each value
// below it that decodes through a wire struct.
func (s *wireStruct) emit() {
	if s.emitted {
		return
	}
	s.emitted = true
	for _, it := range s.items {
		if it.mixin != nil && it.mixin.s.carriesMember() {
			it.mixin.s.emit()
		}
		if it.field != nil {
			for _, c := range it.field.children {
				if c.nonTrivial {
					c.emit()
				}
			}
		}
	}
}

// carriesMember reports whether s, or a mixin below it, carries a JSON member.
func (s *wireStruct) carriesMember() bool {
	for _, it := range s.items {
		if it.field != nil || it.mixin.s.carriesMember() {
			return true
		}
	}
	return false
}

// wireFor returns the wire struct m's handler decodes its body into, "" when it decodes straight
// into the request type.
func (p *bodyPlan) wireFor(m *ast.Method) string {
	if s := p.tops[m]; s != nil {
		return s.name
	}
	return ""
}

// names returns every wire struct's name.
func (p *bodyPlan) names() []string {
	var out []string
	for _, s := range p.order {
		if s.emitted {
			out = append(out, s.name)
		}
	}
	return out
}

// nameWords returns the words a wire struct's name is built from: the type's name, then its
// arguments'.
func (p *bodyPlan) nameWords(n *ast.NamedTypeRef, env *typeEnv) []string {
	if a, ok := env.param(n); ok {
		return p.refWords(a.ref, a.env)
	}
	words := []string{idents.PascalCase(n.Name.Parts[len(n.Name.Parts)-1])}
	for _, arg := range n.Args {
		words = append(words, p.refWords(arg, env)...)
	}
	return words
}

// refWords is [bodyPlan.nameWords] for a type reference: a map's key and value, and List per
// array dimension.
func (p *bodyPlan) refWords(t *ast.TypeRef, env *typeEnv) []string {
	var words []string
	if t.Map != nil {
		words = append([]string{"Map"}, p.refWords(t.Map.Key, env)...)
		words = append(words, p.refWords(t.Map.Value, env)...)
	} else {
		words = p.nameWords(t.Named, env)
	}
	for range t.ArrayDepth {
		words = append(words, "List")
	}
	return words
}

// declared spells a declared type's name, written in env, as imports spells it from the segment's
// package: a type parameter as its argument.
func (p *bodyPlan) declared(imports *importSet, env *typeEnv) func(string) string {
	return func(name string) string {
		if a, ok := env.params[name]; ok && !strings.Contains(name, ".") {
			return p.goTypeWith(imports, a.ref, a.env)
		}
		return imports.qualify(p.viewName(name, env.home))
	}
}

// viewName spells name, written in package home, as the segment's package does.
func (p *bodyPlan) viewName(name, home string) string {
	pkg, sym, qualified := strings.Cut(name, ".")
	switch {
	case !qualified && home == p.view:
		return name
	case !qualified:
		return home + "." + name
	case pkg == p.view:
		return sym
	}
	return name
}

// viewRef spells t, written in env, as the segment's package does.
func (p *bodyPlan) viewRef(t *ast.TypeRef, env *typeEnv) *ast.TypeRef {
	if t == nil || t.Named == nil || t.Named.Name == nil || env.home == p.view || prims.Is(t.Named.Name.String()) {
		return t
	}
	clone, named, name := *t, *t.Named, *t.Named.Name
	name.Parts = strings.Split(p.viewName(name.String(), env.home), ".")
	named.Name, clone.Named = &name, &named
	return &clone
}

// goType spells the request type's Go type of t, written in env.
func (p *bodyPlan) goType(t *ast.TypeRef, env *typeEnv) string {
	return p.goTypeWith(p.imports, t, env)
}

// goTypeWith is [bodyPlan.goType] through imports.
func (p *bodyPlan) goTypeWith(imports *importSet, t *ast.TypeRef, env *typeEnv) string {
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) { imports.importBuiltin(n.Name.String()) })
	return goType(t, semantic.NewResolver(p.proj, env.home), p.declared(imports, env))
}

// wireType spells the wire Go type of t, written in env: [bodyPlan.goType] with each struct that
// decodes through a wire struct named as it.
func (p *bodyPlan) wireType(t *ast.TypeRef, env *typeEnv) string {
	var s string
	if t.Map != nil {
		s = "map[" + p.goType(t.Map.Key, env) + "]" + p.wireType(t.Map.Value, env)
	} else {
		s = p.wireNamed(t.Named, env)
	}
	s = strings.Repeat("[]", t.ArrayDepth) + s
	if t.Optional && !semantic.NewResolver(p.proj, env.home).ResolveTypeRef(t).IsNilable {
		s = "*" + s
	}
	return s
}

// wireNamed is [bodyPlan.wireType] for a named type.
func (p *bodyPlan) wireNamed(n *ast.NamedTypeRef, env *typeEnv) string {
	if a, ok := env.param(n); ok {
		return p.wireType(a.ref, a.env)
	}
	if _, td := p.structDecl(n, env); td != nil {
		if s := p.byKey[p.key(n, env, nil)]; s != nil && s.nonTrivial {
			return s.name
		}
	}
	n.WalkNamedRefs(func(r *ast.NamedTypeRef) { p.imports.importBuiltin(r.Name.String()) })
	return goNamedType(n, semantic.NewResolver(p.proj, env.home), p.declared(p.imports, env))
}

// nilable reports whether the Go value of t, written in env, holds nil itself.
func (p *bodyPlan) nilable(t *ast.TypeRef, env *typeEnv) bool {
	if t.Optional || t.Map != nil || t.ArrayDepth > 0 {
		return true
	}
	if a, ok := env.param(t.Named); ok {
		return p.nilable(a.ref, a.env)
	}
	return semantic.NewResolver(p.proj, env.home).ResolveTypeRef(t).IsNilable
}

// source renders bodyFile's source from the package clause on; "" when no request decodes
// through a wire struct.
func (p *bodyPlan) source(pkgName string) string {
	var decls []string
	for _, s := range p.order {
		if s.emitted {
			decls = append(decls, p.renderStruct(s))
		}
	}
	if len(decls) == 0 {
		return ""
	}
	parts := []string{"package " + pkgName + "\n"}
	if decl := p.imports.decl(); decl != "" {
		parts = append(parts, decl)
	}
	return strings.Join(append(parts, decls...), "\n")
}

// renderStruct renders s's wire struct and its copy method.
func (p *bodyPlan) renderStruct(s *wireStruct) string {
	s.ref.WalkNamedRefs(func(n *ast.NamedTypeRef) { p.imports.importBuiltin(n.Name.String()) })
	pub := goNamedType(s.ref, semantic.NewResolver(p.proj, s.env.home), p.declared(p.imports, s.env))
	var fields, stmts strings.Builder
	for _, it := range s.items {
		if mx := it.mixin; mx != nil {
			if !mx.s.carriesMember() {
				continue
			}
			fmt.Fprintf(&fields, "\t%s\n", mx.s.name)
			fmt.Fprintf(&stmts, "if _p := w.%s.to(&dst.%s); _p != \"\" {\nreturn _p\n}\n", mx.s.name, mx.embed)
			continue
		}
		m := it.field
		typ, copyStmts := p.renderMember(s, m)
		fmt.Fprintf(&fields, "\t%s %s %s\n", m.goName, typ, jsonTagLiteral(m.json))
		stmts.WriteString(copyStmts)
	}
	return fmt.Sprintf(`// %[1]s is the wire form of %[2]s: each member is nil while absent or null.
type %[1]s struct {
%[3]s}

// to copies w into dst, filling the default of each member absent or null, and returns the JSON
// path of the first required member w lacks, or "".
func (w *%[1]s) to(dst *%[2]s) string {
%[4]sreturn ""
}
`, s.name, pub, fields.String(), stmts.String())
}

// jsonTagLiteral is the struct tag carrying JSON key name.
func jsonTagLiteral(name string) string {
	tag := "json:" + strconv.Quote(name)
	if strings.Contains(tag, "`") {
		return strconv.Quote(tag)
	}
	return "`" + tag + "`"
}

// renderMember returns the wire type of m, a member of s, and the statements copying it into dst.
func (p *bodyPlan) renderMember(s *wireStruct, m *wireMember) (typ, stmts string) {
	env := s.inner
	src, dst, key := "w."+m.goName, "dst."+m.goName, strconv.Quote(m.json)
	valWire, valPub := p.wireType(m.value, env), p.goType(m.value, env)
	if m.raw {
		p.imports.use(rawImportPath)
		valWire, valPub = rawGoType, rawGoType
	}
	nilable := m.raw || p.nilable(m.value, env)
	nullable := func() string {
		p.imports.use(serverImport)
		return "server.Nullable[" + valWire + "]"
	}
	missing := fmt.Sprintf("if %s == nil {\nreturn %s\n}\n", src, key)
	switch {
	case m.skipped:
		if m.pubPtr || !nilable {
			return "*" + valWire, ""
		}
		return valWire, ""
	case m.def != nil:
		// Absent and null both read as nil, and both take the default.
		lit := renderDefault(p.viewRef(m.value, env), m.def.Value, p.r, p.imports)
		typ, val := valWire, src
		if !nilable {
			typ, val = "*"+valWire, deref(src)
		}
		take, fill := p.copyValue(m.value, env, val, dst, m.json, 0, true), dst+" = "+lit+"\n"
		switch {
		case m.pubPtr && !nilable && valWire == valPub:
			take = dst + " = " + src + "\n"
		case m.pubPtr:
			take = dst + " = new(" + valPub + ")\n" + p.copyValue(m.value, env, val, deref(dst), m.json, 0, true)
		}
		if m.pubPtr {
			fill = "_d := " + lit + "\n" + dst + " = &_d\n"
		}
		return typ, fmt.Sprintf("if %s != nil {\n%s} else {\n%s}\n", src, take, fill)
	case m.presence == wire.JSONRequired && m.raw:
		return valWire, fmt.Sprintf("if len(%[1]s) == 0 || string(%[1]s) == \"null\" {\nreturn %[2]s\n}\n%[3]s = %[1]s\n", src, key, dst)
	case m.presence == wire.JSONRequired && nilable:
		return valWire, missing + p.copyValue(m.value, env, src, dst, m.json, 0, true)
	case m.presence == wire.JSONRequired:
		return "*" + valWire, missing + p.copyValue(m.value, env, deref(src), dst, m.json, 0, true)
	case m.presence == wire.JSONNullable && m.raw:
		return valWire, fmt.Sprintf("if len(%[1]s) == 0 {\nreturn %[2]s\n}\n%[3]s = %[1]s\n", src, key, dst)
	case m.presence == wire.JSONNullable:
		take := p.copyValue(m.value, env, "(*_v)", dst, m.json, 0, true)
		switch {
		case m.pubPtr && valWire == valPub:
			take = dst + " = _v\n"
		case m.pubPtr:
			take = dst + " = new(" + valPub + ")\n" + p.copyValue(m.value, env, "(*_v)", deref(dst), m.json, 0, true)
		}
		return nullable(), fmt.Sprintf("if !%s.Present() {\nreturn %s\n}\nif _v := %s.Value(); _v != nil {\n%s}\n", src, key, src, take)
	case m.pubPtr && valWire == valPub:
		return "*" + valWire, dst + " = " + src + "\n"
	case m.pubPtr:
		return "*" + valWire, fmt.Sprintf("if %s != nil {\n%s = new(%s)\n%s}\n", src, dst, valPub, p.copyValue(m.value, env, deref(src), deref(dst), m.json, 0, true))
	}
	return valWire, p.copyValue(m.value, env, src, dst, m.json, 0, false)
}

// copyValue renders the statements copying src, a wire value of type t written in env, into dst,
// both addressable; a required member missing below returns its path under the member key.
// nonNil says src holds a value, so a list, map or pointer needs no nil check.
func (p *bodyPlan) copyValue(t *ast.TypeRef, env *typeEnv, src, dst, key string, depth int, nonNil bool) string {
	if p.wireType(t, env) == p.goType(t, env) {
		return bare(dst) + " = " + bare(src) + "\n"
	}
	guard := func(body string) string {
		if nonNil {
			return body
		}
		return "if " + bare(src) + " != nil {\n" + body + "}\n"
	}
	if t.Optional {
		inner := *t
		inner.Optional = false
		if semantic.NewResolver(p.proj, env.home).ResolveTypeRef(t).IsNilable {
			return p.copyValue(&inner, env, src, dst, key, depth, nonNil)
		}
		return guard(fmt.Sprintf("%s = new(%s)\n%s", bare(dst), p.goType(&inner, env),
			p.copyValue(&inner, env, deref(bare(src)), deref(bare(dst)), key, depth+1, true)))
	}
	switch {
	case t.ArrayDepth > 0:
		i := fmt.Sprintf("_i%d", depth)
		return guard(fmt.Sprintf("%[1]s = make(%[2]s, len(%[3]s))\nfor %[4]s := range %[3]s {\n%[5]s}\n",
			bare(dst), p.goType(t, env), bare(src), i, p.copyValue(t.ElemTypeRef(), env, src+"["+i+"]", dst+"["+i+"]", key, depth+1, false)))
	case t.Map != nil:
		k, e, d := fmt.Sprintf("_k%d", depth), fmt.Sprintf("_e%d", depth), fmt.Sprintf("_d%d", depth)
		return guard(fmt.Sprintf("%[1]s = make(%[2]s, len(%[3]s))\nfor %[4]s, %[5]s := range %[3]s {\nvar %[6]s %[7]s\n%[8]s%[9]s[%[4]s] = %[6]s\n}\n",
			bare(dst), p.goType(t, env), bare(src), k, e, d, p.goType(t.Map.Value, env), p.copyValue(t.Map.Value, env, e, d, key, depth+1, false), dst))
	}
	if a, ok := env.param(t.Named); ok {
		return p.copyValue(a.ref, a.env, src, dst, key, depth, nonNil)
	}
	return fmt.Sprintf("if _p := %s.to(%s); _p != \"\" {\nreturn %s + _p\n}\n", pointee(src), addr(dst), strconv.Quote(key+"."))
}

// deref is the value pointer expression ptr points to, parenthesized so an index applies to it.
func deref(ptr string) string { return "(*" + ptr + ")" }

// derefOf returns the pointer e, a [deref] of it, dereferences.
func derefOf(e string) (string, bool) {
	if strings.HasPrefix(e, "(*") && strings.HasSuffix(e, ")") {
		return e[2 : len(e)-1], true
	}
	return "", false
}

// bare drops the parentheses [deref] puts around e where e stands alone.
func bare(e string) string {
	if ptr, ok := derefOf(e); ok {
		return "*" + ptr
	}
	return e
}

// addr is the address of e, an addressable value.
func addr(e string) string {
	if ptr, ok := derefOf(e); ok {
		return ptr
	}
	return "&" + e
}

// pointee is e as the receiver of a pointer method: the pointer itself for a [deref].
func pointee(e string) string {
	if ptr, ok := derefOf(e); ok {
		return ptr
	}
	return e
}
