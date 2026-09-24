package semantic

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// mixinEmbed is where a mixin first embedded under a Go field name.
type mixinEmbed struct {
	full string
	pos  lexer.Position
}

// goEmbedName returns the Go embedded-field name of a mixin ref: its last
// segment, so `shared.Leaf` and `Leaf` both embed as `Leaf`.
func goEmbedName(n *ast.QualifiedIdent) string {
	if n == nil || len(n.Parts) == 0 {
		return ""
	}
	return n.Parts[len(n.Parts)-1]
}

// duplicateEmbedMsg words the duplicate-embed diagnostic for one ref
// embedded twice or for two refs with the same Go field name.
func duplicateEmbedMsg(first, second, leaf string) string {
	if first == second {
		return fmt.Sprintf("mixin %q is embedded more than once - the generated Go struct would declare it twice and fail to compile (%q redeclared)", first, leaf)
	}
	return fmt.Sprintf("mixins %q and %q both embed as the Go field %q - the generated struct would redeclare it and fail to compile", first, second, leaf)
}

// fieldEmbedClash is a field whose Go field name equals an embedded
// mixin's type name.
type fieldEmbedClash struct {
	pos    lexer.Position
	field  string
	goName string
	mixin  string
}

// fieldEmbedClashes returns each field whose Go field name equals an
// embedded mixin's type name: `type Host { Page  page int }`.
func fieldEmbedClashes(body []ast.TypeMember) []fieldEmbedClash {
	embeds := map[string]bool{}
	for _, m := range body {
		mx, ok := m.(*ast.Mixin)
		if !ok || mx.Ref == nil || mx.Ref.Name == nil || len(mx.Ref.Name.Parts) == 0 {
			continue
		}
		embeds[mx.Ref.Name.Parts[len(mx.Ref.Name.Parts)-1]] = true
	}
	var out []fieldEmbedClash
	for _, f := range ast.Fields(body) {
		if gn := idents.GoFieldName(f.Name); embeds[gn] {
			out = append(out, fieldEmbedClash{pos: f.Pos, field: f.Name, goName: gn, mixin: gn})
		}
	}
	return out
}

// checkMixins validates the mixins of every type and error body. A field
// name reachable twice through embedding is an error, not Go-style shadowing.
func (a *analyzer) checkMixins() {
	for _, td := range a.pkg.Types {
		a.checkOneTypeMixins(td.Name, td.Body)
		a.checkTypeParamMixin(td.Name, td.TypeParams, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.checkOneTypeMixins(ed.Name, ed.Body)
	}
}

// typeParamMixin is a mixin that embeds a type parameter of its host.
type typeParamMixin struct {
	pos   lexer.Position
	param string
}

// findTypeParamMixins returns every mixin in body that embeds a type
// parameter of the host (`type Box<T> { T }`), which Go cannot embed.
func findTypeParamMixins(typeParams []string, body []ast.TypeMember) []typeParamMixin {
	if len(typeParams) == 0 {
		return nil
	}
	tp := map[string]bool{}
	for _, p := range typeParams {
		tp[p] = true
	}
	var out []typeParamMixin
	for _, m := range body {
		mx, ok := m.(*ast.Mixin)
		if !ok || mx.Ref == nil || mx.Ref.Name == nil {
			continue
		}
		if parts := mx.Ref.Name.Parts; len(parts) == 1 && tp[parts[0]] {
			out = append(out, typeParamMixin{pos: mx.Pos, param: parts[0]})
		}
	}
	return out
}

// typeParamMixinMsg words the embedded-type-parameter diagnostic.
func typeParamMixinMsg(host, param string) string {
	return fmt.Sprintf(
		"type %s cannot embed its type parameter %q as a mixin: Go forbids embedding a type parameter, so the generated struct would not compile. Use a named field instead (e.g. `value %s`).",
		host, param, param)
}

// checkTypeParamMixin rejects an embedded type parameter.
func (a *analyzer) checkTypeParamMixin(host string, typeParams []string, body []ast.TypeMember) {
	for _, tpm := range findTypeParamMixins(typeParams, body) {
		a.diag(tpm.pos, tpm.pos, lexer.SeverityError, CodeMixinConflict, "%s", typeParamMixinMsg(host, tpm.param))
	}
}

// fieldOrigin is where a field name first appeared while expanding a host:
// the field's declaration and the host or mixin that brought it in.
type fieldOrigin struct {
	pos  lexer.Position
	from string // host name or mixin chain root
}

// reportGoNameCollisions reports fields with different DSL names but one Go
// name that come from different origins in seen: Go field promotion cannot
// tell them apart. Fields of one origin share a struct, where the duplicate
// Go name gets a numeric suffix.
func reportGoNameCollisions(seen map[string]fieldOrigin, emit func(pos lexer.Position, msg string)) {
	type ent struct {
		dsl  string
		from string
		pos  lexer.Position
	}
	groups := map[string][]ent{}
	for dsl, o := range seen {
		gn := idents.GoFieldName(dsl)
		groups[gn] = append(groups[gn], ent{dsl: dsl, from: o.from, pos: o.pos})
	}
	for _, gn := range slices.Sorted(maps.Keys(groups)) {
		ents := groups[gn]
		if len(ents) < 2 {
			continue
		}
		// The lowest (from, dsl) is the anchor the others are reported against.
		sort.Slice(ents, func(i, j int) bool {
			if ents[i].from != ents[j].from {
				return ents[i].from < ents[j].from
			}
			return ents[i].dsl < ents[j].dsl
		})
		first := ents[0]
		for _, e := range ents[1:] {
			if e.from == first.from {
				continue // one struct: the Go name gets a suffix
			}
			emit(e.pos, fmt.Sprintf(
				"field %q (from %s) and field %q (from %s) both lower to the Go field %q across mixin embedding - Go field promotion can't tell them apart, so the generated binder / validator / writers would target one field for both. Rename one.",
				e.dsl, e.from, first.dsl, first.from, gn))
		}
	}
}

// checkOneTypeMixins checks one body's mixins: duplicate embeds, field
// conflicts through each mixin, fields named like an embed, and Go-name
// collisions across embeds.
func (a *analyzer) checkOneTypeMixins(host string, body []ast.TypeMember) {
	seen := map[string]fieldOrigin{}
	emit := func(pos lexer.Position, code, format string, args ...any) *Diagnostic {
		return a.diag(pos, pos, lexer.SeverityError, code, format, args...)
	}
	// Host fields first, so a mixin's same-named field is the one reported.
	for _, f := range ast.Fields(body) {
		if _, dup := seen[f.Name]; dup {
			continue // already reported by checkFieldUniqueness
		}
		seen[f.Name] = fieldOrigin{pos: f.Pos, from: host}
	}
	seenMixin := map[string]mixinEmbed{}
	for _, m := range body {
		mx, ok := m.(*ast.Mixin)
		if !ok {
			continue
		}
		if mx.Ref != nil && mx.Ref.Name != nil {
			leaf := goEmbedName(mx.Ref.Name)
			full := mx.Ref.Name.String()
			if prev, dup := seenMixin[leaf]; dup {
				d := emit(mx.Pos, CodeMixinConflict, "%s", duplicateEmbedMsg(prev.full, full, leaf))
				d.Related = related(prev.pos, "first embedded here")
				continue
			}
			seenMixin[leaf] = mixinEmbed{full: full, pos: mx.Pos}
		}
		a.processMixin(host, mx, seen)
	}
	for _, c := range fieldEmbedClashes(body) {
		emit(c.pos, CodeMixinConflict,
			"field %q collides with the embedded mixin %q: both become the Go field %q in the generated struct. Rename the field.",
			c.field, c.mixin, c.goName)
	}
	reportGoNameCollisions(seen, func(pos lexer.Position, msg string) {
		emit(pos, CodeMixinConflict, "%s", msg)
	})
}

// processMixin resolves one mixin, checks its generic arity and expands its
// fields into seen. An unresolved name is left to the type-reference checks.
func (a *analyzer) processMixin(host string, mx *ast.Mixin, seen map[string]fieldOrigin) {
	if mx.Ref == nil || mx.Ref.Name == nil {
		return
	}
	pkg, name := a.proj.resolve(a.pkg.Name, mx.Ref.Name)
	if pkg == nil {
		return
	}
	td := a.resolveMixinTarget(mx, pkg, name)
	if td == nil {
		return
	}
	if len(mx.Ref.Args) != len(td.TypeParams) {
		a.diag(mx.Pos, mx.Pos, lexer.SeverityError, CodeMixinArity,
			"mixin %s expects %d generic argument(s), got %d",
			mx.Ref.Name.String(), len(td.TypeParams), len(mx.Ref.Args))
		return
	}
	// Seeding the host makes a self-mixin a cycle.
	visited := map[string]bool{a.pkg.Name + "." + host: true}
	a.collectMixinFields(pkg, name, mx.Ref.Name.String(), mx.Pos, seen, visited)
}

// mixinNamedKinds are the kinds a mixin's name is resolved among: a type,
// or a declaration [analyzer.resolveMixinTarget] rejects as no type.
const mixinNamedKinds = TypeDecls | EnumDecls | ErrorDecls | ScalarDecls | MiddlewareDecls

// resolveMixinTarget returns the type name declares in pkg; a name of
// another kind is reported as [CodeMixinNonType].
func (a *analyzer) resolveMixinTarget(mx *ast.Mixin, pkg *Package, name string) *ast.TypeDecl {
	kind := ""
	switch d := pkg.Decl(name, mixinNamedKinds).(type) {
	case *ast.TypeDecl:
		return d
	case *ast.EnumDecl:
		kind = "an enum"
	case *ast.ErrorDecl:
		kind = "an error"
	case *ast.ScalarDecl:
		kind = "a scalar"
	case *ast.MiddlewareDecl:
		kind = "a middleware"
	default:
		return nil
	}
	a.diag(mx.Pos, mx.Pos, lexer.SeverityError, CodeMixinNonType,
		"mixin %s is %s, not a type", a.refDisplay(pkg.Name, name), kind)
	return nil
}

// collectMixinFields adds the fields of type name in pkg and its nested
// mixins to seen, reporting conflicts and cycles at mixinPos, the host's
// mixin. A bare nested mixin resolves in its embedding type's package;
// visited holds the expansion stack.
func (a *analyzer) collectMixinFields(
	pkg *Package,
	name, sourceLabel string,
	mixinPos lexer.Position,
	seen map[string]fieldOrigin,
	visited map[string]bool,
) {
	key := pkg.Name + "." + name
	if visited[key] {
		a.diag(mixinPos, mixinPos, lexer.SeverityError, CodeMixinCycle,
			"mixin %s forms a cycle", a.refDisplay(pkg.Name, name))
		return
	}
	visited[key] = true
	defer delete(visited, key)

	td, ok := pkg.Types[name]
	if !ok {
		return
	}
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			if prev, dup := seen[v.Name]; dup {
				if prev.from == sourceLabel {
					continue // reached twice through the same host mixin
				}
				diag := a.diag(mixinPos, mixinPos, lexer.SeverityError,
					CodeMixinConflict,
					"mixin %s adds field %q, which conflicts with %s",
					sourceLabel, v.Name, prev.from)
				diag.Related = related(prev.pos, "first field declared here")
				continue
			}
			seen[v.Name] = fieldOrigin{pos: v.Pos, from: sourceLabel}
		case *ast.Mixin:
			if v.Ref == nil {
				continue
			}
			if next, sym := a.proj.resolve(pkg.Name, v.Ref.Name); next != nil {
				a.collectMixinFields(next, sym, sourceLabel, mixinPos, seen, visited)
			}
		}
	}
}
