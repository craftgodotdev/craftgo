// Wire-name and binding-decorator combination checks: duplicate wire names
// (explicit and auto-bound), single-binding, and overlapping bound forms.
package semantic

import (
	"maps"
	"slices"
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

// wireBindingSite records one explicitly wire-bound field found while
// flattening a request/error body across packages: its position, field name,
// binding kind and wire name, and whether it was promoted through a
// cross-package mixin (and so is invisible to the per-package pass).
type wireBindingSite struct {
	pos      lexer.Position
	field    string
	kind     string
	name     string
	promoted bool
}

// checkProjectDuplicateWireNames re-runs the explicit duplicate-wire-name
// check with cross-package visibility. The per-package
// [analyzer.checkDuplicateWireNames] flattens only same-package mixins
// ([analyzer.flattenRequestFields] skips qualified refs), so a wire name
// bound by a field promoted through a CROSS-package mixin escapes it: the
// binder then reads one wire value into two fields and the OpenAPI advertises
// a duplicate parameter - a direct types<->transport<->spec disagreement. This
// pass expands cross-package mixin bodies exactly as the codegen binder does
// and reports a collision only when at least one of the two fields was
// promoted from another package, so it never double-reports the same-package
// collisions the per-package pass already covers.
func (r *refResolver) checkProjectDuplicateWireNames() {
	for _, pkgName := range slices.Sorted(maps.Keys(r.proj.Packages)) {
		pkg := r.proj.Packages[pkgName]
		if pkg == nil {
			continue
		}
		current := pkgName
		lookup := func(name string) *ast.TypeDecl {
			if i := strings.LastIndexByte(name, '.'); i >= 0 {
				if p := r.proj.Packages[name[:i]]; p != nil {
					return p.Types[name[i+1:]]
				}
				return nil
			}
			if p := r.proj.Packages[current]; p != nil {
				return p.Types[name]
			}
			return nil
		}
		for _, name := range slices.Sorted(maps.Keys(pkg.Types)) {
			td := pkg.Types[name]
			r.reportCrossPkgWireCollisions(td.Name, td.Body, lookup)
		}
		for _, name := range slices.Sorted(maps.Keys(pkg.Errors)) {
			ed := pkg.Errors[name]
			r.reportCrossPkgWireCollisions(ed.Name, ed.Body, lookup)
		}
	}
}

// reportCrossPkgWireCollisions flattens one type/error body across packages
// and emits a [CodeDuplicateWireName] diagnostic for each wire-name reuse
// whose two fields do not both live in this package (a same-package clash is
// the per-package pass's job). The first field bound to a given wire name
// anchors the "first bound here" back-reference. The key formula matches
// [analyzer.checkDuplicateWireNames] so the two passes agree on what collides.
func (r *refResolver) reportCrossPkgWireCollisions(parent string, body []ast.TypeMember, lookup func(string) *ast.TypeDecl) {
	var sites []wireBindingSite
	collectWireBindingSites(body, "", parent, map[string]bool{}, lookup, &sites)
	seen := map[string]wireBindingSite{}
	for _, s := range sites {
		key := s.kind + "\x00" + wire.CanonicalWireName(s.kind, s.name)
		prev, dup := seen[key]
		if !dup {
			seen[key] = s
			continue
		}
		if s.promoted || prev.promoted {
			d := r.diag(s.pos, lexer.SeverityError, CodeDuplicateWireName,
				"%s.%s: @%s(%q) reuses a wire name already bound on this request through a cross-package mixin - the OpenAPI would carry a duplicate parameter and the binder would read both fields from one value. Use distinct names.",
				parent, s.field, s.kind, s.name)
			d.Related = related(prev.pos, "first bound here")
		}
	}
}

// collectWireBindingSites appends every explicitly wire-bound field in body to
// out, descending into mixin targets resolved through lookup the same way
// [walkBodyForPath] does. prefix is the package a bare mixin resolves against
// (empty for the body's own package); any field reached under a non-empty
// prefix was promoted from another package and so is invisible to the
// per-package duplicate-wire check. visited keys on label to stop cyclic mixin
// graphs and to collapse diamond embeds to a single visit.
func collectWireBindingSites(body []ast.TypeMember, prefix, label string, visited map[string]bool, lookup func(string) *ast.TypeDecl, out *[]wireBindingSite) {
	if visited[label] {
		return
	}
	visited[label] = true
	promoted := prefix != ""
	for _, mem := range body {
		switch v := mem.(type) {
		case *ast.Field:
			if kind, name, bound := wireBinding(v); bound {
				*out = append(*out, wireBindingSite{pos: v.Pos, field: v.Name, kind: kind, name: name, promoted: promoted})
			}
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			// Resolve the mixin in the package it lives in: a qualified ref
			// names that package; a bare ref nested inside a foreign mixin
			// inherits that mixin's package (the prefix).
			key := v.Ref.Name.String()
			childPrefix := prefix
			if len(v.Ref.Name.Parts) == 2 {
				childPrefix = v.Ref.Name.Parts[0]
			} else if prefix != "" {
				key = prefix + "." + key
			}
			if next := lookup(key); next != nil {
				collectWireBindingSites(next.Body, childPrefix, key, visited, lookup, out)
			}
		}
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
	first := ""
	var firstPos lexer.Position
	for _, d := range f.Decorators {
		if !wire.IsBindingName(d.Name) {
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
