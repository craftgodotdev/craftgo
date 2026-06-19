// Wire-name and binding-decorator combination checks: duplicate wire names
// (explicit and auto-bound), single-binding, and overlapping bound forms.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkDuplicateWireNames rejects two EXPLICITLY wire-bound fields that share a
// wire name on one source (request / response / error). The body is flattened
// so a binding promoted through a same-package mixin is seen, and header names
// are case-folded so `X-Trace` / `x-trace` collide. Auto-promoted bindings are
// route/verb-dependent and handled per-method by [analyzer.checkDuplicateAutoWireNames].
func (a *analyzer) checkDuplicateWireNames(parent string, members []ast.TypeMember) {
	seen := map[string]lexer.Position{}
	for _, f := range a.flattenRequestFields(members, map[string]bool{}) {
		kind, name, bound := wireBinding(f)
		if !bound {
			continue
		}
		key := kind + "\x00" + wire.CanonicalWireName(kind, name)
		if prev, dup := seen[key]; dup {
			d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateWireName,
				"%s.%s: @%s(%q) reuses a wire name already bound on the same source - the OpenAPI would carry a duplicate parameter and the binder would read both fields from one value. Use distinct names.",
				parent, f.Name, kind, name)
			d.Related = related(prev, "first bound here")
			continue
		}
		seen[key] = f.Pos
	}
}

// checkDuplicateAutoWireNames catches a wire-name collision that involves an
// AUTO-bound field - an undecorated field promoted to @path (its name matches
// a {segment}) or to @query (on a body-less verb). The per-declaration
// [analyzer.checkDuplicateWireNames] sees only EXPLICIT decorators, so an
// auto-bound field colliding with an explicit (or another auto) binding slips
// through into a silent double-read + a duplicate OpenAPI parameter. This runs
// in method context (route segments + verb) where the auto-binding is known,
// and reports only collisions involving an auto-bound field (explicit/explicit
// is already covered) so the two checks never double-report.
func (a *analyzer) checkDuplicateAutoWireNames(m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil {
		return
	}
	td, ok := a.pkg.Types[m.Request.Name.String()]
	if !ok {
		return // cross-package request - not modelled here
	}
	pathSegs := MethodRoutePathVars(m, a.pkg.Services)
	bodyVerb := wire.IsBodyVerb(m.Verb)
	reqName := m.Request.Name.String()
	type binding struct {
		pos  lexer.Position
		auto bool
	}
	seen := map[string]binding{}
	for _, f := range a.flattenRequestFields(td.Body, map[string]bool{}) {
		kind, auto := wire.RequestFieldBinding(f, pathSegs, bodyVerb)
		switch kind {
		case wire.BindingPath, wire.BindingQuery, wire.BindingHeader, wire.BindingCookie, wire.BindingForm:
		default:
			continue
		}
		name := wire.WireName(f, kind)
		key := kind + "\x00" + wire.CanonicalWireName(kind, name)
		if prev, dup := seen[key]; dup {
			if auto || prev.auto {
				d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateWireName,
					"%s.%s on %s %s: this field auto-binds to @%s(%q), already bound by another field - the binder reads both from one value and the OpenAPI carries a duplicate parameter. Give one an explicit, distinct binding.",
					reqName, f.Name, strings.ToUpper(m.Verb), m.Name, kind, name)
				d.Related = related(prev.pos, "first bound here")
			}
			continue
		}
		seen[key] = binding{pos: f.Pos, auto: auto}
	}
}

// wireBinding returns the wire (kind, name) a field binds to: the binding
// decorator's explicit string arg, or the field name when the arg is
// absent. bound is false for an unbound (body) field.
func wireBinding(f *ast.Field) (kind, name string, bound bool) {
	switch k := wire.BindingKind(f.Decorators); k {
	case wire.BindingPath, wire.BindingQuery, wire.BindingHeader, wire.BindingCookie, wire.BindingForm:
		return k, wire.WireName(f, k), true
	default:
		return "", "", false
	}
}

// checkBoundOverlap warns when both the closed-form bound
// (`@length(min, max)` / `@range(min, max)`) and one of its one-sided
// equivalents (`@minLength`/`@maxLength`, `@gt*`/`@lt*`) appear on the
// same field. The bound interpretation in OpenAPI is well-defined - the
// validator path applies every constraint, so the bounds AND together -
// but two equivalent forms make the source noisy and the canonical form
// ambiguous. Warn (not error) and let the user pick.
func (a *analyzer) checkBoundOverlap(parent string, f *ast.Field) {
	if f == nil {
		return
	}
	for _, d := range f.Decorators {
		if d == nil {
			continue
		}
		var partners []string
		switch d.Name {
		case "length":
			partners = []string{"minLength", "maxLength"}
		case "range":
			partners = []string{"gt", "gte", "lt", "lte"}
		default:
			continue
		}
		for _, p := range f.Decorators {
			if p == nil || p == d {
				continue
			}
			for _, want := range partners {
				if p.Name != want {
					continue
				}
				a.diag(p.Pos, decoratorEnd(p), lexer.SeverityWarning, CodeDecoratorRedundant,
					"field %s.%s: @%s overlaps with @%s on the same field; pick one form for clarity",
					parent, f.Name, p.Name, d.Name)
			}
		}
	}
}

// checkSingleBinding enforces the "at most one binding" rule. The
// six binding decorators (`@path / @query / @header / @cookie / @body /
// @form`) are mutually exclusive; the first wins, every subsequent one
// gets a diagnostic with a back-reference to the first.
func (a *analyzer) checkSingleBinding(parent string, f *ast.Field) {
	bindings := map[string]bool{
		wire.BindingPath: true, wire.BindingQuery: true, wire.BindingHeader: true,
		wire.BindingCookie: true, wire.BindingBody: true, wire.BindingForm: true,
	}
	first := ""
	var firstPos lexer.Position
	for _, d := range f.Decorators {
		if !bindings[d.Name] {
			continue
		}
		if first == "" {
			first = d.Name
			firstPos = d.Pos
			continue
		}
		diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingConflict,
			"field %s.%s: @%s conflicts with @%s (a field must have at most one binding)",
			parent, f.Name, d.Name, first)
		diag.Related = related(firstPos, "first binding here")
	}
}
