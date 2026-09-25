package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// Binding-type diagnostic messages.
const (
	msgBindPath        = "field %s.%s: @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s"
	msgBindWire        = "field %s.%s: @%s requires string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or generic instantiations) - got %s"
	msgBindCookieArray = "field %s.%s: @cookie cannot bind to an array - cookies carry a single value per name"
	msgBindForm        = "field %s.%s: @form requires `file` or string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or a single-level array of those, `file[]` included (no maps, structs, or nested arrays) - got %s"
)

// checkBindingFieldType rejects a wire binding whose field type the binder
// cannot fill, `@nullable` on any wire binding, and `@default` on `@path`.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field) {
	kind, _ := wire.BindingKind(f.Decorators)
	if f.Type == nil || !kind.IsParam() {
		return
	}
	d := ast.FindDecorator(f.Decorators, kind.String())
	switch {
	case ast.HasDecorator(f.Decorators, "nullable"):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
			"@nullable cannot be combined with @%s: a wire parameter is a string with no JSON-null form. Use `?` to make the parameter optional.",
			d.Name)
	case kind == wire.BindPath && ast.HasDecorator(f.Decorators, "default"):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
			"@default cannot be combined with @path: a path segment is always supplied for a matched route, so the default can never apply - drop it.")
	case f.Type.ArrayDepth > 1 && (kind == wire.BindQuery || kind == wire.BindHeader || kind == wire.BindForm):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
			"field %s.%s: @%s cannot bind to a multi-dimensional array - a wire parameter carries repeated single values (`?x=1&x=2`), which has no nested form. Move the field to the JSON body or flatten to a single-level array.",
			parent, f.Name, d.Name)
	case kind == wire.BindPath && !a.isPathBindingType(f.Type):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindPath, parent, f.Name, f.Type)
	case kind == wire.BindCookie && f.Type.Array:
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindCookieArray, parent, f.Name)
	case (kind == wire.BindQuery || kind == wire.BindHeader || kind == wire.BindCookie) && !a.isWireBindingType(f.Type):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindWire, parent, f.Name, d.Name, f.Type)
	case kind == wire.BindForm && !a.isFormBindingType(f.Type):
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, msgBindForm, parent, f.Name, f.Type)
	}
}

// isPathBindingType reports whether t can bind to `@path`: a wire-bindable
// type that is neither optional nor an array.
func (a *analyzer) isPathBindingType(t *ast.TypeRef) bool {
	return a.pathBindableIn(a.pkg.Name, t)
}

// pathBindableIn is [analyzer.isPathBindingType] with bare type names
// resolved in homePkg.
func (a *analyzer) pathBindableIn(homePkg string, t *ast.TypeRef) bool {
	if t == nil || t.Optional || t.Array {
		return false
	}
	return a.wireBindableIn(homePkg, t)
}

// isWireBindingType reports whether t can bind to a query, header or cookie:
// a parseable primitive, a scalar or enum over one, or a 1-D array of them.
func (a *analyzer) isWireBindingType(t *ast.TypeRef) bool {
	return a.wireBindableIn(a.pkg.Name, t)
}

// wireBindableIn is [analyzer.isWireBindingType] with bare type names
// resolved in homePkg - the package of the type that declares the field.
func (a *analyzer) wireBindableIn(homePkg string, t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Args) > 0 || t.ArrayDepth > 1 {
		return false
	}
	switch rt := a.elemFacts(homePkg, t); rt.Category {
	case CatPrimitive, CatScalar:
		return prims.IsWireParseable(rt.ResolvedPrim)
	case CatEnum:
		return true
	}
	return false
}

// isFormBindingType reports whether t can bind to `@form`: a wire-bindable
// type, a `file`, or a 1-D `file[]`.
func (a *analyzer) isFormBindingType(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil {
		return false
	}
	if a.elemFacts(a.pkg.Name, t).Category == CatFile {
		return t.ArrayDepth <= 1
	}
	return a.isWireBindingType(t)
}

// elemFacts resolves t in homePkg, or the element of t when t is an array.
func (a *analyzer) elemFacts(homePkg string, t *ast.TypeRef) ResolvedField {
	if t.Array {
		t = t.ElemTypeRef()
	}
	return resolveTypeRef(t, false, a.proj.Packages[homePkg], a.proj)
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

// wireKey is a binding and a canonical wire name, which two fields must not share.
type wireKey struct {
	binding wire.Binding
	name    string
}

// checkDuplicateWireNames rejects two explicitly bound fields of a body, mixins
// included, with one binding kind and wire name; header names ignore case.
func (a *analyzer) checkDuplicateWireNames(parent string, members []ast.TypeMember) {
	fields, _ := a.proj.flattenFields(a.pkg.Name, a.pkg.Name, members, nil, nil, nil)
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
		if !kind.IsParam() {
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
	if k, _ := wire.BindingKind(f.Decorators); k.IsParam() {
		return k, wire.WireName(f, k), true
	}
	return wire.BindBody, "", false
}

// checkAutoPathField checks each request field of m that auto-binds to a
// route variable.
func (a *analyzer) checkAutoPathField(m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	view, fields, ok := a.requestFields(m)
	if !ok {
		return
	}
	pathSegs := methodRoutePathVars(m, a.pkg.Services)
	if len(pathSegs) == 0 {
		return
	}
	reqName := m.Request.Name.String()
	for _, ff := range fields {
		a.autoPathFieldRule(reqName, view, pathSegs, ff.Field)
	}
}

// autoPathFieldRule rejects `?`, `@nullable`, `@default` or a non-path type on
// a field auto-bound to @path; the field's type resolves in package view.
func (a *analyzer) autoPathFieldRule(reqName, view string, pathSegs map[string]bool, f *ast.Field) {
	if f == nil || f.Type == nil {
		return
	}
	if b, auto := wire.RequestFieldBinding(f, pathSegs, false); b != wire.BindPath || !auto {
		return
	}
	switch {
	case f.Type.Optional:
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which a matched route always supplies - drop the optional `?` (a path parameter is never absent).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "nullable"):
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, but @nullable makes it a pointer while the path binder writes a plain string - drop @nullable (a path parameter has no null form).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "default"):
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which is always supplied, so @default can never apply - drop it.",
			reqName, f.Name, f.Name)
	case !a.pathBindableIn(view, f.Type):
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s auto-binds to the path segment {%s}, but @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
			reqName, f.Name, f.Name, f.Type.String())
	}
}

// checkFilePosition rejects a `file` field below the top level of a request
// type, which the multipart binder never reaches, and any `file[][]`.
func (a *analyzer) checkFilePosition() {
	for _, td := range a.pkg.Types {
		a.checkFileArrayDepth(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.checkFileArrayDepth(ed.Name, ed.Body)
	}
	reported := map[lexer.Position]bool{}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			view, fields, ok := a.requestFields(m)
			if !ok {
				continue
			}
			seen := map[string]bool{}
			for _, ff := range fields {
				a.reportNestedFiles(view, ff.Field.Type, m.Request.Name.String()+"."+ff.Field.Name, seen, reported)
			}
		}
	}
}

// checkFileArrayDepth rejects a `file[][]` field of the body of owner.
func (a *analyzer) checkFileArrayDepth(owner string, body []ast.TypeMember) {
	for _, f := range ast.Fields(body) {
		if isFileTypeRef(f.Type) && f.Type.ArrayDepth > 1 {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
				"field %s.%s: a multi-dimensional `file` array (`file[][]`) has no multipart encoding - only a single `file` or a 1-D `file[]` is supported", owner, f.Name)
		}
	}
}

// reportNestedFiles reports each `file` field of the struct types t reaches,
// mixin fields included, and of the structs below them; t is spelled as
// package view spells it and path names how the request reaches it.
func (a *analyzer) reportNestedFiles(view string, t *ast.TypeRef, path string, seen map[string]bool, reported map[lexer.Position]bool) {
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		pkg, sym := a.proj.resolve(view, n.Name)
		if pkg == nil || pkg.Types[sym] == nil || seen[pkg.Name+"."+sym] {
			return
		}
		seen[pkg.Name+"."+sym] = true
		td := pkg.Types[sym]
		fields, _ := a.proj.flattenFields(view, pkg.Name, td.Body, td.TypeParams, nil, nil)
		for _, ff := range fields {
			f := ff.Field
			if !isFileTypeRef(f.Type) {
				a.reportNestedFiles(view, f.Type, path+"."+f.Name, seen, reported)
				continue
			}
			if reported[f.Pos] {
				continue
			}
			reported[f.Pos] = true
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
				"field %s.%s: a `file` field nested inside a request body (reached through %s) is not bindable - the multipart binder reads only top-level request fields; move the `file` to the top level of the request type (or carry it in via a mixin)", td.Name, f.Name, path)
		}
	})
}

// isFileTypeRef reports whether t names `file`, optional or in an array.
func isFileTypeRef(t *ast.TypeRef) bool {
	return t != nil && t.Named != nil && t.Named.Name != nil && t.Named.Name.String() == "file"
}
