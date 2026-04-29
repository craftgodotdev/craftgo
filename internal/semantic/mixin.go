package semantic

// Mixin field expansion + conflict detection. Per README §"Mixin", a
// bare qualified ident inside a type body embeds the referenced type's
// fields into the host. The DSL is composition-only — there's no
// `extends` keyword — so the validation here mirrors Go's struct
// embedding rules with one extra constraint: a name collision is a
// hard error rather than promotion shadowing.
//
// Diagnostic codes:
//
//   - [CodeMixinUnresolved]    — name doesn't resolve to anything in
//     the package.
//   - [CodeMixinNonType]       — name resolves to a non-type entity
//     (enum, error, scalar, middleware).
//   - [CodeMixinCycle]         — A mixes B mixes A.
//   - [CodeMixinConflict]      — two paths bring in the same field.
//   - [CodeMixinArity]         — generic mixin args disagree with the
//     target type's [TypeParams].
//
// Generic mixin substitution (`Page<User>` → fields with T replaced by
// User) is not modelled in detail here — for conflict detection we
// only care about field NAMES, which are stable across substitution.
// Codegen does the actual T→User rewrite per-instance.

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// checkMixins walks every type and error body, validating mixins and
// collecting an "all reachable field names" set for conflict detection.
// Field-name uniqueness within the host's own body is already
// enforced by [analyzer.checkFieldUniqueness]; this pass adds the
// mixin-aware view.
func (a *analyzer) checkMixins() {
	for _, td := range a.pkg.Types {
		a.checkOneTypeMixins(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.checkOneTypeMixins(ed.Name, ed.Body)
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
	for _, m := range body {
		mx, ok := m.(*ast.Mixin)
		if !ok {
			continue
		}
		a.processMixin(host, mx, seen)
	}
}

// processMixin validates one mixin reference against the package and
// expands its fields into seen. visited is initialised with the host
// so a self-mixin is detected immediately as a cycle.
func (a *analyzer) processMixin(host string, mx *ast.Mixin, seen map[string]fieldOrigin) {
	if mx.Ref == nil || mx.Ref.Name == nil {
		return
	}
	if len(mx.Ref.Name.Parts) != 1 {
		// Qualified — already flagged by [analyzer.checkQualifiedRefs];
		// skipping here avoids a duplicate diagnostic on the same span.
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
// anchor — we underline the host's `MixinName` token, not the
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
					// Same mixin path bringing in the same field —
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
