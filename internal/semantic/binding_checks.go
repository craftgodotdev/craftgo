package semantic

import (
	"fmt"
	"maps"
	"slices"
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
// A @header or @cookie field typed by one of typeParams, the declaration's
// type parameters, is checked where the type is instantiated.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field, typeParams []string) {
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
	case (kind == wire.BindHeader || kind == wire.BindCookie) && typeParamNamed(f.Type, typeParams):
	default:
		if msg := a.proj.wireTypeFault(parent, a.pkg.Name, f, kind); msg != "" {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType, "%s", msg)
		}
	}
}

// wireTypeFault says why field f of parent, whose type package home spells,
// cannot ride binding kind, or returns "".
func (p *Project) wireTypeFault(parent, home string, f *ast.Field, kind wire.Binding) string {
	t := f.Type
	switch {
	case t.ArrayDepth > 1 && (kind == wire.BindQuery || kind == wire.BindHeader || kind == wire.BindForm):
		return fmt.Sprintf("field %s.%s: @%s cannot bind to a multi-dimensional array - a wire parameter carries repeated single values (`?x=1&x=2`), which has no nested form. Move the field to the JSON body or flatten to a single-level array.",
			parent, f.Name, kind)
	case kind == wire.BindPath && !p.pathBindable(home, t):
		return fmt.Sprintf(msgBindPath, parent, f.Name, t)
	case kind == wire.BindCookie && t.Array:
		return fmt.Sprintf(msgBindCookieArray, parent, f.Name)
	case (kind == wire.BindQuery || kind == wire.BindHeader || kind == wire.BindCookie) && !p.wireBindable(home, t):
		return fmt.Sprintf(msgBindWire, parent, f.Name, kind, t)
	case kind == wire.BindForm && !p.formBindable(home, t):
		return fmt.Sprintf(msgBindForm, parent, f.Name, t)
	}
	return ""
}

// typeParamNamed reports whether t names one of typeParams, whatever its
// suffixes.
func typeParamNamed(t *ast.TypeRef, typeParams []string) bool {
	return t.Map == nil && t.Named != nil && t.Named.Name != nil && len(t.Named.Name.Parts) == 1 &&
		slices.Contains(typeParams, t.Named.Name.Parts[0])
}

// checkTypeParamWireBindings rejects, at each request, response or error
// mixin that instantiates a type, a @header or @cookie field typed by one
// of the type's parameters whose argument cannot ride the binding.
func (a *analyzer) checkTypeParamWireBindings() {
	for _, svcName := range a.pkg.ServiceNames() {
		si := a.pkg.Services[svcName]
		for _, m := range si.Methods {
			rawReq, rawResp := wire.RawSides(si.Decorators(m))
			if m.Request != nil {
				a.checkInstanceWireBindings(m.Request, m.Request.Pos, false, rawReq)
			}
			if m.Response != nil {
				a.checkInstanceWireBindings(m.Response.Type, m.Response.Pos, true, rawResp)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(a.pkg.Errors)) {
		for _, member := range a.pkg.Errors[name].Body {
			if mx, ok := member.(*ast.Mixin); ok {
				a.checkInstanceWireBindings(mx.Ref, mx.Pos, true, false)
			}
		}
	}
}

// checkInstanceWireBindings reports at pos each @header or @cookie field of
// the type ref names, typed by a type parameter, whose argument cannot ride
// the binding; raw says logic reads or writes the headers, so no generated
// binding holds the value. An argument naming no type is left to the
// reference check, and one holding a `file` to [analyzer.checkFilePosition]
// when fileReported says it reports every `file` at pos.
func (a *analyzer) checkInstanceWireBindings(ref *ast.NamedTypeRef, pos lexer.Position, fileReported, raw bool) {
	view, fields, ok := a.instanceFields(ref)
	if !ok {
		return
	}
	for _, ff := range fields {
		kind, _ := wire.BindingKind(ff.Field.Decorators)
		if !ff.paramTyped || (kind != wire.BindHeader && kind != wire.BindCookie) {
			continue
		}
		if a.proj.namesNoType(view, ff.Field.Type) || (fileReported && holdsFile(ff.Field.Type)) {
			continue
		}
		msg := a.proj.wireTypeFault(ref.String(), view, ff.Field, kind)
		if msg == "" && ff.sliceBehindPointer && !raw {
			msg = fmt.Sprintf("field %s.%s: @%s rides an optional type parameter over an array, whose Go value is a pointer to a slice no %s binding reads or writes - drop the `?` from the type parameter (an array is already nilable)",
				ref, ff.Field.Name, kind, kind)
		}
		if msg != "" {
			a.diag(pos, pos, lexer.SeverityError, CodeBindingType, "%s", msg)
		}
	}
}

// pathBindable reports whether t, as package home spells it, can bind a
// route variable: a wire-bindable type that is neither optional nor an array.
func (p *Project) pathBindable(home string, t *ast.TypeRef) bool {
	if t == nil || t.Optional || t.Array {
		return false
	}
	return p.wireBindable(home, t)
}

// wireBindable reports whether t, as package home spells it, can bind to a
// query, header or cookie: a parseable primitive, a scalar or enum over one,
// or a 1-D array of them.
func (p *Project) wireBindable(home string, t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil || len(t.Named.Args) > 0 || t.ArrayDepth > 1 {
		return false
	}
	switch rt := p.elemFacts(home, t); rt.Category {
	case CatPrimitive, CatScalar:
		return prims.IsWireParseable(rt.ResolvedPrim)
	case CatEnum:
		return true
	}
	return false
}

// formBindable reports whether t, as package home spells it, can bind to
// `@form`: a wire-bindable type, a `file`, or a 1-D `file[]`.
func (p *Project) formBindable(home string, t *ast.TypeRef) bool {
	if t == nil || t.Named == nil {
		return false
	}
	if p.elemFacts(home, t).Category == CatFile {
		return t.ArrayDepth <= 1
	}
	return p.wireBindable(home, t)
}

// namesNoType reports whether t, as package home spells it, names a type
// that neither a built-in nor a declaration provides, through map keys and
// values and generic arguments.
func (p *Project) namesNoType(home string, t *ast.TypeRef) bool {
	missing := false
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		if n.Name == nil || prims.Is(n.Name.String()) {
			return
		}
		if pkg, sym := p.resolve(home, n.Name); pkg == nil || pkg.Decl(sym, TypeRefDecls) == nil {
			missing = true
		}
	})
	return missing
}

// elemFacts resolves t in home, or the element of t when t is an array.
func (p *Project) elemFacts(home string, t *ast.TypeRef) ResolvedField {
	if t.Array {
		t = t.ElemTypeRef()
	}
	return resolveTypeRef(t, false, p.Packages[home], p)
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
	_, fields, ok := a.instanceFields(m.Request)
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
	view, fields, ok := a.instanceFields(m.Request)
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

// pathFault is what keeps a field from binding a route variable.
type pathFault uint8

const (
	pathBinds    pathFault = iota
	pathOptional           // `T?`
	pathNullable           // @nullable
	pathDefault            // @default
	pathType               // a type no route variable parses into
)

// pathFaultOf returns what keeps f, whose type package home spells, from
// binding a route variable.
func (p *Project) pathFaultOf(home string, f *ast.Field) pathFault {
	switch {
	case f.Type.Optional:
		return pathOptional
	case ast.HasDecorator(f.Decorators, "nullable"):
		return pathNullable
	case ast.HasDecorator(f.Decorators, "default"):
		return pathDefault
	case !p.pathBindable(home, f.Type):
		return pathType
	}
	return pathBinds
}

// PathParam returns the route variable request field f binds: its @path
// name, or its own name when it carries no binding decorator and no
// @sensitive; f's type resolves in package home. ok is false when f binds
// none or binding one is an error.
func (p *Project) PathParam(home string, f *ast.Field) (name string, ok bool) {
	if f == nil || f.Type == nil {
		return "", false
	}
	if b, _ := wire.RequestFieldBinding(f, map[string]bool{f.Name: true}, false); b != wire.BindPath {
		return "", false
	}
	return wire.WireName(f, wire.BindPath), p.pathFaultOf(home, f) == pathBinds
}

// autoPathFieldRule rejects a field auto-bound to @path that
// [Project.pathFaultOf] faults; the field's type resolves in package view.
func (a *analyzer) autoPathFieldRule(reqName, view string, pathSegs map[string]bool, f *ast.Field) {
	if f == nil || f.Type == nil {
		return
	}
	if b, auto := wire.RequestFieldBinding(f, pathSegs, false); b != wire.BindPath || !auto {
		return
	}
	switch a.proj.pathFaultOf(view, f) {
	case pathOptional:
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which a matched route always supplies - drop the optional `?` (a path parameter is never absent).",
			reqName, f.Name, f.Name)
	case pathNullable:
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, but @nullable makes it a pointer while the path binder writes a plain string - drop @nullable (a path parameter has no null form).",
			reqName, f.Name, f.Name)
	case pathDefault:
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which is always supplied, so @default can never apply - drop it.",
			reqName, f.Name, f.Name)
	case pathType:
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
			"field %s.%s auto-binds to the path segment {%s}, but @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
			reqName, f.Name, f.Name, f.Type.String())
	}
}

// checkFilePosition rejects a `file` no generated code carries: below the
// top level of a request type, which the multipart binder never reaches, or
// anywhere in a response, an error body or an event payload; and a type's
// `file[][]` field.
func (a *analyzer) checkFilePosition() {
	for _, td := range a.pkg.Types {
		a.checkFileArrayDepth(td.Name, td.Body)
	}
	a.checkRequestFiles()
	a.checkResponseFiles()
	a.checkErrorBodyFiles()
	a.checkPayloadFiles()
}

// msgFileArrayDepth ends a diagnostic about a `file` array of two or more
// dimensions.
const msgFileArrayDepth = "a multi-dimensional `file` array has no multipart encoding - only a single `file` or a 1-D `file[]` is supported"

// checkFileArrayDepth rejects a `file[][]` field of the body of owner.
func (a *analyzer) checkFileArrayDepth(owner string, body []ast.TypeMember) {
	for _, f := range ast.Fields(body) {
		if isFileTypeRef(f.Type) && f.Type.ArrayDepth > 1 {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
				"field %s.%s is `%s`: "+msgFileArrayDepth, owner, f.Name, f.Type)
		}
	}
}

// checkRequestFiles reports each field of a request, top-level fields
// excepted when they are a `file` or a `file[]`, that holds a `file`.
func (a *analyzer) checkRequestFiles() {
	reported := map[lexer.Position]bool{}
	report := func(owner string, f *ast.Field, where string) {
		if reported[f.Pos] {
			return
		}
		reported[f.Pos] = true
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
			"field %s.%s holds a `file` the multipart binder never reads%s - it binds only a request's top-level `file` and `file[]` fields; move the `file` there (a mixin may carry it)", owner, f.Name, where)
	}
	for _, svcName := range a.pkg.ServiceNames() {
		for _, m := range a.pkg.Services[svcName].Methods {
			view, fields, ok := a.instanceFields(m.Request)
			if !ok {
				continue
			}
			reqName := m.Request.Name.String()
			seen := map[string]bool{}
			for _, ff := range fields {
				f := ff.Field
				switch {
				case isFileTypeRef(f.Type):
					if ff.paramTyped && f.Type.ArrayDepth > 1 {
						a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodeFilePosition,
							"request %s of %s.%s types field %s as `%s`: "+msgFileArrayDepth, m.Request, svcName, m.Name, f.Name, f.Type)
					}
				case holdsFile(f.Type):
					report(reqName, f, "")
				default:
					a.visitFileHolders(view, f.Type, reqName+"."+f.Name, seen, func(owner string, file *ast.Field, path string) {
						report(owner, file, " (reached through "+path+")")
					})
				}
			}
		}
	}
}

// checkResponseFiles rejects each response clause whose type holds a `file`.
func (a *analyzer) checkResponseFiles() {
	for _, svcName := range a.pkg.ServiceNames() {
		for _, m := range a.pkg.Services[svcName].Methods {
			if m.Response == nil || m.Response.Type == nil {
				continue
			}
			resp := m.Response.Type
			if at := a.fileAt(&ast.TypeRef{Pos: m.Response.Pos, Named: resp}, resp.String()); at != "" {
				a.diag(m.Response.Pos, m.Response.Pos, lexer.SeverityError, CodeFilePosition,
					"response %s of %s.%s holds a `file` at %s, but only a request carries an upload - send the content as `bytes`",
					resp, svcName, m.Name, at)
			}
		}
	}
}

// checkErrorBodyFiles rejects each field and mixin of an error body that
// holds a `file`.
func (a *analyzer) checkErrorBodyFiles() {
	for _, name := range slices.Sorted(maps.Keys(a.pkg.Errors)) {
		ed := a.pkg.Errors[name]
		for _, member := range ed.Body {
			var at string
			var pos lexer.Position
			switch v := member.(type) {
			case *ast.Field:
				pos, at = v.Pos, ed.Name+"."+v.Name
				if !holdsFile(v.Type) {
					at = a.fileAt(v.Type, at)
				}
			case *ast.Mixin:
				pos, at = v.Pos, a.fileAt(&ast.TypeRef{Pos: v.Pos, Named: v.Ref}, ed.Name)
			}
			if at != "" {
				a.diag(pos, pos, lexer.SeverityError, CodeFilePosition,
					"error %s holds a `file` at %s, but only a request carries an upload - send the content as `bytes`", ed.Name, at)
			}
		}
	}
}

// checkPayloadFiles rejects each event payload whose type holds a `file`.
func (a *analyzer) checkPayloadFiles() {
	for _, name := range slices.Sorted(maps.Keys(a.pkg.Events)) {
		d := a.pkg.Events[name]
		if d.Payload == nil || d.Payload.Type == nil {
			continue
		}
		payload := d.Payload.Type
		if at := a.fileAt(&ast.TypeRef{Pos: d.Payload.Pos, Named: payload}, payload.String()); at != "" {
			a.diag(d.Payload.Pos, d.Payload.Pos, lexer.SeverityError, CodeFilePosition,
				"payload %s of event %s holds a `file` at %s, but only a request carries an upload - send the content as `bytes`",
				payload, d.Name, at)
		}
	}
}

// fileAt returns the path, from path, to the first field holding a `file`
// among those of the struct types t reaches and of the structs below them;
// "" when there is none. t is spelled as the analyser's package spells it.
func (a *analyzer) fileAt(t *ast.TypeRef, path string) string {
	at := ""
	a.visitFileHolders(a.pkg.Name, t, path, map[string]bool{}, func(_ string, f *ast.Field, p string) {
		if at == "" {
			at = p + "." + f.Name
		}
	})
	return at
}

// visitFileHolders calls visit with each field that [holdsFile] among the
// fields of the struct types t reaches, mixin fields included, and of the
// structs below them: owner is the struct reached and path how t reaches
// it. An instance whose type arguments hold a `file` reaches it through the
// fields its type parameters type. t is spelled as package view spells it.
func (a *analyzer) visitFileHolders(view string, t *ast.TypeRef, path string, seen map[string]bool, visit func(owner string, f *ast.Field, path string)) {
	inFileArgs := map[*ast.NamedTypeRef]bool{}
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		if slices.ContainsFunc(n.Args, holdsFile) {
			for _, arg := range n.Args {
				arg.WalkNamedRefs(func(m *ast.NamedTypeRef) { inFileArgs[m] = true })
			}
		}
	})
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		pkg, sym := a.proj.resolve(view, n.Name)
		if inFileArgs[n] || pkg == nil || pkg.Types[sym] == nil {
			return
		}
		key, args := pkg.Name+"."+sym, []*ast.TypeRef(nil)
		if slices.ContainsFunc(n.Args, holdsFile) {
			key, args = pkg.Name+"."+n.String(), n.Args
		}
		if seen[key] {
			return
		}
		seen[key] = true
		td := pkg.Types[sym]
		fields, _ := a.proj.flattenFields(view, pkg.Name, td.Body, td.TypeParams, args, nil)
		for _, ff := range fields {
			f := ff.Field
			if holdsFile(f.Type) {
				visit(td.Name, f, path)
				continue
			}
			a.visitFileHolders(view, f.Type, path+"."+f.Name, seen, visit)
		}
	})
}

// holdsFile reports whether t names `file` itself, optional or in an array,
// or as a map key or value or a generic argument, at any depth.
func holdsFile(t *ast.TypeRef) bool {
	found := false
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		found = found || (n.Name != nil && n.Name.String() == "file")
	})
	return found
}

// isFileTypeRef reports whether t names `file`, optional or in an array.
func isFileTypeRef(t *ast.TypeRef) bool {
	return t != nil && t.Named != nil && t.Named.Name != nil && t.Named.Name.String() == "file"
}
