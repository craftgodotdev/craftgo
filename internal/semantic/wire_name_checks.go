package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// wireKey is a binding and a canonical wire name, which two fields must not share.
type wireKey struct {
	binding wire.Binding
	name    string
}

// checkDuplicateWireNames rejects two explicitly bound fields of a body, mixins
// included, with one binding kind and wire name; header names ignore case.
func (a *analyzer) checkDuplicateWireNames(parent string, members []ast.TypeMember) {
	fields, _ := a.proj.flattenFields(a.pkg.Name, a.pkg.Name, members, nil, nil)
	seen := map[wireKey]FlatField{}
	for _, ff := range fields {
		f := ff.Field
		kind, name, bound := wireBinding(f)
		if !bound {
			continue
		}
		key := wireKey{kind, wire.CanonicalWireName(kind, name)}
		prev, dup := seen[key]
		if !dup {
			seen[key] = ff
			continue
		}
		msg := "%s.%s: @%s(%q) reuses a wire name already bound on the same source - the OpenAPI would carry a duplicate parameter and the binder would read both fields from one value. Use distinct names."
		if ff.Home != a.pkg.Name || prev.Home != a.pkg.Name {
			msg = "%s.%s: @%s(%q) reuses a wire name already bound on this request through a cross-package mixin - the OpenAPI would carry a duplicate parameter and the binder would read both fields from one value. Use distinct names."
		}
		d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateWireName, msg, parent, f.Name, kind, name)
		d.Related = related(prev.Field.Pos, "first bound here")
	}
}

// checkDuplicateAutoWireNames rejects a wire-name collision in m's request
// that involves an auto-bound field; explicit pairs are reported per body.
func (a *analyzer) checkDuplicateAutoWireNames(m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil {
		return
	}
	_, fields, ok := a.requestFields(m)
	if !ok {
		return
	}
	pathSegs := methodRoutePathVars(m, a.pkg.Services)
	bodyVerb := wire.IsBodyVerb(m.Verb)
	reqName := m.Request.Name.String()
	type binding struct {
		pos  lexer.Position
		auto bool
	}
	seen := map[wireKey]binding{}
	for _, ff := range fields {
		f := ff.Field
		kind, auto := wire.RequestFieldBinding(f, pathSegs, bodyVerb)
		switch kind {
		case wire.BindPath, wire.BindQuery, wire.BindHeader, wire.BindCookie, wire.BindForm:
		default:
			continue
		}
		name := wire.WireName(f, kind)
		key := wireKey{kind, wire.CanonicalWireName(kind, name)}
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

// wireBinding returns the binding and wire name of f's explicit binding;
// bound is false for a body field.
func wireBinding(f *ast.Field) (kind wire.Binding, name string, bound bool) {
	switch k, _ := wire.BindingKind(f.Decorators); k {
	case wire.BindPath, wire.BindQuery, wire.BindHeader, wire.BindCookie, wire.BindForm:
		return k, wire.WireName(f, k), true
	default:
		return wire.BindBody, "", false
	}
}

// checkBoundOverlap warns when `@length` or `@range` shares a field with one
// of its one-sided forms.
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

// checkSingleBinding rejects every binding decorator on f after the first.
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
