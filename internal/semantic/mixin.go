package semantic

// Mixin field expansion + conflict detection. Per README §"Mixin", a
// bare qualified ident inside a type body embeds the referenced type's
// fields into the host. The DSL is composition-only - there's no
// `extends` keyword - so the validation here mirrors Go's struct
// embedding rules with one extra constraint: a name collision is a
// hard error rather than promotion shadowing.
//
// Diagnostic codes:
//
//   - [CodeMixinUnresolved]    - name doesn't resolve to anything in
//     the package.
//   - [CodeMixinNonType]       - name resolves to a non-type entity
//     (enum, error, scalar, middleware).
//   - [CodeMixinCycle]         - A mixes B mixes A.
//   - [CodeMixinConflict]      - two paths bring in the same field.
//   - [CodeMixinArity]         - generic mixin args disagree with the
//     target type's [TypeParams].
//
// Generic mixin substitution (`Page<User>` → fields with T replaced by
// User) is not modelled in detail here - for conflict detection we
// only care about field NAMES, which are stable across substitution.
// Codegen does the actual T→User rewrite per-instance.

import (
	"fmt"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// mixinEmbed records where a mixin first embedded under a given Go
// field name, for the duplicate-embed diagnostic.
type mixinEmbed struct {
	full string
	pos  lexer.Position
}

// goEmbedName returns the Go embedded-field name a mixin ref lowers to:
// the unqualified last segment, so `shared.Leaf` and a local `Leaf` both
// yield `Leaf` (and would redeclare it in the generated struct).
func goEmbedName(n *ast.QualifiedIdent) string {
	if n == nil || len(n.Parts) == 0 {
		return ""
	}
	return n.Parts[len(n.Parts)-1]
}

// duplicateEmbedMsg builds the duplicate-embed diagnostic, distinguishing
// the exact-same-ref case from two distinct refs (local vs imported, or
// two imports) that collapse to the same Go field name.
func duplicateEmbedMsg(first, second, leaf string) string {
	if first == second {
		return fmt.Sprintf("mixin %q is embedded more than once — the generated Go struct would declare it twice and fail to compile (%q redeclared)", first, leaf)
	}
	return fmt.Sprintf("mixins %q and %q both embed as the Go field %q — the generated struct would redeclare it and fail to compile", first, second, leaf)
}

// fieldEmbedClash is a field whose Go field-name equals an embedded
// mixin's type name.
type fieldEmbedClash struct {
	pos    lexer.Position
	field  string
	goName string
	mixin  string
}

// fieldEmbedClashes returns each field whose generated Go field-name
// collides with an embedded mixin's type name. The mixin embeds as that
// type name, so the struct would declare the same Go identifier twice
// (`type Host { Page  page int }` → a `Page` embed and a `Page` field →
// "Page redeclared"). JSON tags differ, so OpenAPI is unaffected, but the
// Go output does not compile.
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
	for _, m := range body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		if gn := idents.GoFieldName(f.Name); embeds[gn] {
			out = append(out, fieldEmbedClash{pos: f.Pos, field: f.Name, goName: gn, mixin: gn})
		}
	}
	return out
}

// checkMixins walks every type and error body, validating mixins and
// collecting an "all reachable field names" set for conflict detection.
// Field-name uniqueness within the host's own body is already
// enforced by [analyzer.checkFieldUniqueness]; this pass adds the
// mixin-aware view.
func (a *analyzer) checkMixins() {
	for _, td := range a.pkg.Types {
		a.checkOneTypeMixins(td.Name, td.Body)
		a.checkTypeParamMixin(td.Name, td.TypeParams, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.checkOneTypeMixins(ed.Name, ed.Body)
	}
}

// typeParamMixin is a mixin that embeds a bare type-parameter of its host
// generic, with the source position for the diagnostic.
type typeParamMixin struct {
	pos   lexer.Position
	param string
}

// findTypeParamMixins returns every mixin in body that embeds a bare
// type-parameter of the host generic (`type Box<T> { T }`). Go forbids
// embedding a type parameter ("embedded field type cannot be a (pointer to a)
// type parameter"), so the generated struct would never compile. Shared by the
// per-package and project mixin passes so both reject it identically.
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

// typeParamMixinMsg is the shared diagnostic text for an embedded type-param.
func typeParamMixinMsg(host, param string) string {
	return fmt.Sprintf(
		"type %s cannot embed its type parameter %q as a mixin: Go forbids embedding a type parameter, so the generated struct would not compile. Use a named field instead (e.g. `value %s`).",
		host, param, param)
}

// checkTypeParamMixin rejects an embedded type-parameter at the per-package
// pass; [refResolver.checkProjectMixins] mirrors it for project mode.
func (a *analyzer) checkTypeParamMixin(host string, typeParams []string, body []ast.TypeMember) {
	for _, tpm := range findTypeParamMixins(typeParams, body) {
		a.diag(tpm.pos, tpm.pos, lexer.SeverityError, CodeMixinConflict, "%s", typeParamMixinMsg(host, tpm.param))
	}
}

// fieldOrigin records where a field name first surfaced when expanding
// a host's mixins. The pos points at the original field declaration so
// IDE conflict messages link back to the real source line; the from
// label distinguishes "host's own" vs "via mixin X".
type fieldOrigin struct {
	pos  lexer.Position
	from string // host name or mixin chain root
}

// reportGoNameCollisions flags fields whose DSL names differ but whose Go
// identifiers collide ACROSS an embed boundary. Within one struct's own
// fields a Go-name collision is dedup-renamed by codegen (`UserID` /
// `UserID_2`), but a host field and a field promoted from a mixin — or two
// fields promoted from different mixins — land in SEPARATE Go structs that
// Go field-promotion merges by name: the binder, validator, and response
// writers all read `v.UserID`, targeting one field for both (clobber) or, for
// two equal-depth embeds, producing an ambiguous selector that won't compile.
// The codegen dedup runs per declaring struct and so cannot fix it; reject at
// design time so the author renames one. seen holds every contributing field
// (host's own + promoted) keyed by DSL name with its origin; a Go-name group
// is safe only when all its members share one origin. emit anchors each
// diagnostic at the colliding field. Shared by the per-package
// ([analyzer.checkOneTypeMixins]) and project ([refResolver.checkOneTypeMixinsProject]) passes.
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
	gnames := make([]string, 0, len(groups))
	for gn := range groups {
		gnames = append(gnames, gn)
	}
	sort.Strings(gnames)
	for _, gn := range gnames {
		ents := groups[gn]
		if len(ents) < 2 {
			continue
		}
		// Deterministic order: the lowest (from, dsl) is the anchor the
		// others are reported against.
		sort.Slice(ents, func(i, j int) bool {
			if ents[i].from != ents[j].from {
				return ents[i].from < ents[j].from
			}
			return ents[i].dsl < ents[j].dsl
		})
		first := ents[0]
		for _, e := range ents[1:] {
			if e.from == first.from {
				continue // same declaring struct — codegen dedups it
			}
			emit(e.pos, fmt.Sprintf(
				"field %q (from %s) and field %q (from %s) both lower to the Go field %q across mixin embedding — Go field promotion can't tell them apart, so the generated binder / validator / writers would target one field for both. Rename one.",
				e.dsl, e.from, first.dsl, first.from, gn))
		}
	}
}

// checkOneTypeMixins validates every top-level mixin in body, walking
// nested mixins recursively. The `seen` map carries (fieldName →
// origin) for the host plus all already-expanded mixins.
func (a *analyzer) checkOneTypeMixins(host string, body []ast.TypeMember) {
	seen := map[string]fieldOrigin{}
	// Host's own fields land first; they always win if a later mixin
	// brings the same name in (the conflict is reported, never
	// silently overridden).
	for _, m := range body {
		if f, ok := m.(*ast.Field); ok {
			if _, dup := seen[f.Name]; dup {
				continue // already reported by checkFieldUniqueness
			}
			seen[f.Name] = fieldOrigin{pos: f.Pos, from: host}
		}
	}
	seenMixin := map[string]mixinEmbed{}
	for _, m := range body {
		mx, ok := m.(*ast.Mixin)
		if !ok {
			continue
		}
		if mx.Ref != nil && mx.Ref.Name != nil {
			// Key on the Go embedded-field name (the unqualified leaf), not
			// the dotted ref: `Leaf` and `shared.Leaf` both embed as the
			// field `Leaf` and would redeclare it.
			leaf := goEmbedName(mx.Ref.Name)
			full := mx.Ref.Name.String()
			if prev, dup := seenMixin[leaf]; dup {
				d := a.diag(mx.Pos, mx.Pos, lexer.SeverityError, CodeMixinConflict, "%s", duplicateEmbedMsg(prev.full, full, leaf))
				d.Related = related(prev.pos, "first embedded here")
				continue
			}
			seenMixin[leaf] = mixinEmbed{full: full, pos: mx.Pos}
		}
		a.processMixin(host, mx, seen)
	}
	for _, c := range fieldEmbedClashes(body) {
		a.diag(c.pos, c.pos, lexer.SeverityError, CodeMixinConflict,
			"field %q collides with the embedded mixin %q: both become the Go field %q in the generated struct. Rename the field.",
			c.field, c.mixin, c.goName)
	}
	reportGoNameCollisions(seen, func(pos lexer.Position, msg string) {
		a.diag(pos, pos, lexer.SeverityError, CodeMixinConflict, "%s", msg)
	})
}

// processMixin validates one mixin reference against the package and
// expands its fields into seen. visited is initialised with the host
// so a self-mixin is detected immediately as a cycle.
//
// In project mode the per-package pass is skipped (see
// [Options.skipMixinCheck]); the project-level resolver runs an
// equivalent expansion that ALSO resolves qualified mixin refs
// (`shared.Timestamps`). When this runs per-package, qualified refs
// are silently skipped because we have no cross-package view.
func (a *analyzer) processMixin(host string, mx *ast.Mixin, seen map[string]fieldOrigin) {
	if mx.Ref == nil || mx.Ref.Name == nil {
		return
	}
	if len(mx.Ref.Name.Parts) != 1 {
		// Qualified - either rejected by [analyzer.checkQualifiedRefs]
		// (single-package mode) or expanded by the project resolver
		// (multi-package mode). Either way, do not fire here.
		return
	}
	target := mx.Ref.Name.Parts[0]
	td := a.resolveMixinTarget(mx, target)
	if td == nil {
		return
	}
	// Generic arity.
	if len(mx.Ref.Args) != len(td.TypeParams) {
		a.diag(mx.Pos, mx.Pos, lexer.SeverityError, CodeMixinArity,
			"mixin %s expects %d generic argument(s), got %d",
			target, len(td.TypeParams), len(mx.Ref.Args))
		return
	}
	visited := map[string]bool{host: true}
	a.collectMixinFields(target, target, mx.Pos, seen, visited)
}

// resolveMixinTarget finds the *TypeDecl that target names. Reports a
// distinct diagnostic when the name resolves to a different kind of
// declaration (enum / error / scalar / middleware) so the user sees
// "you mixin'd an enum" rather than a generic "unresolved".
func (a *analyzer) resolveMixinTarget(mx *ast.Mixin, target string) *ast.TypeDecl {
	if td, ok := a.pkg.Types[target]; ok {
		return td
	}
	kind := ""
	switch {
	case a.pkg.Enums[target] != nil:
		kind = "enum"
	case a.pkg.Errors[target] != nil:
		kind = "error"
	case a.pkg.Scalars[target] != nil:
		kind = "scalar"
	case a.pkg.Middlewares[target] != nil:
		kind = "middleware"
	}
	if kind != "" {
		a.diag(mx.Pos, mx.Pos, lexer.SeverityError, CodeMixinNonType,
			"mixin %s is a %s, not a type", target, kind)
		return nil
	}
	a.diag(mx.Pos, mx.Pos, lexer.SeverityError, CodeMixinUnresolved,
		"mixin %s is not declared in this package", target)
	return nil
}

// collectMixinFields walks the body of `name`, accumulating field
// origins into seen. Nested mixins recurse with the same `seen` map so
// one deep conflict surfaces as one diagnostic at the offending
// top-level mixin position. visited tracks the expansion stack to
// catch cycles. Top-level call passes mixinPos as the diagnostic
// anchor - we underline the host's `MixinName` token, not the
// nested decl that actually contains the colliding field.
func (a *analyzer) collectMixinFields(
	name, sourceLabel string,
	mixinPos lexer.Position,
	seen map[string]fieldOrigin,
	visited map[string]bool,
) {
	if visited[name] {
		a.diag(mixinPos, mixinPos, lexer.SeverityError, CodeMixinCycle,
			"mixin %s forms a cycle", name)
		return
	}
	visited[name] = true
	defer delete(visited, name)

	td, ok := a.pkg.Types[name]
	if !ok {
		return
	}
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			if prev, dup := seen[v.Name]; dup {
				if prev.from == sourceLabel {
					// Same mixin path bringing in the same field -
					// nothing to flag.
					continue
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
			if v.Ref == nil || v.Ref.Name == nil || len(v.Ref.Name.Parts) != 1 {
				continue
			}
			a.collectMixinFields(v.Ref.Name.Parts[0], sourceLabel, mixinPos, seen, visited)
		}
	}
}
