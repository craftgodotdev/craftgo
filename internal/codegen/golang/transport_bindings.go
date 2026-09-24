package golang

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// collectResponseBindings renders the writers of m's response @header and @cookie fields;
// needsStrconv reports a non-string value formatted through strconv.
func collectResponseBindings(m *ast.Method, pkg *semantic.Package, r *projectResolver) (headers, cookies []paramBinding, needsStrconv bool) {
	if m.Response == nil || m.Response.Type == nil {
		return nil, nil, false
	}
	td, prefix := semantic.LookupMethodType(m.Response.Type, r.Resolver)
	if td == nil {
		return nil, nil, false
	}
	return responseBindingsFor(td, prefix, "resp", pkg, r)
}

// responseBindingsFor renders the @header and @cookie writers of body td, mixin fields included,
// reading the values from accessVar (`resp`, or `e` for an error body).
func responseBindingsFor(td *ast.TypeDecl, prefix, accessVar string, pkg *semantic.Package, r *projectResolver) (headers, cookies []paramBinding, needsStrconv bool) {
	for _, ff := range flattenFieldsWithNames(td, prefix, r) {
		f := ff.Field
		kind, _ := wire.BindingKind(f.Decorators)
		if kind != wire.BindHeader && kind != wire.BindCookie {
			continue
		}
		stmt, ns := renderResponseWrite(f, pkg, r, kind, accessVar, ff.Name)
		if ns {
			needsStrconv = true
		}
		entry := paramBinding{Bind: stmt}
		switch kind {
		case wire.BindHeader:
			headers = append(headers, entry)
		case wire.BindCookie:
			cookies = append(cookies, entry)
		}
	}
	return headers, cookies, needsStrconv
}

// renderResponseWrite renders the statement writing accessVar.goName as a header or cookie:
// an optional field is nil-guarded and an array header adds one value per element.
func renderResponseWrite(f *ast.Field, pkg *semantic.Package, r *projectResolver, kind wire.Binding, accessVar, goName string) (stmt string, needsStrconv bool) {
	prim, declName := wirePrimName(f, pkg, r)
	wireName := wire.WireName(f, kind)
	field := accessVar + "." + goName

	set := func(valueExpr string) string {
		if kind == wire.BindCookie {
			return fmt.Sprintf("http.SetCookie(w, &http.Cookie{Name: %q, Value: %s})", wireName, valueExpr)
		}
		return fmt.Sprintf("w.Header().Set(%q, %s)", wireName, valueExpr)
	}

	switch {
	case f.Type != nil && f.Type.Array:
		// The analyser rejects a @cookie array, so this is a header.
		expr, ns := formatToString(prim, declName, "_v")
		return fmt.Sprintf("for _, _v := range %s {\nw.Header().Add(%q, %s)\n}", field, wireName, expr), ns
	case f.Type != nil && f.Type.Optional:
		expr, ns := formatToString(prim, declName, "*"+field)
		return fmt.Sprintf("if %s != nil {\n%s\n}", field, set(expr)), ns
	default:
		expr, ns := formatToString(prim, declName, field)
		return set(expr), ns
	}
}

// wirePrimName resolves f's type to the primitive it is formatted as, a scalar to its primitive
// and an enum to "int" or "string", falling back to "string"; declName is f's own type name.
func wirePrimName(f *ast.Field, pkg *semantic.Package, r *projectResolver) (prim, declName string) {
	if f.Type == nil || f.Type.Named == nil {
		return "string", ""
	}
	declName = f.Type.Named.Name.String()
	if prims.IsWireParseable(declName) {
		return declName, declName
	}
	if sc := r.LookupScalar(declName); sc != nil {
		if prims.IsWireParseable(sc.Primitive) {
			return sc.Primitive, declName
		}
	}
	if ed := r.LookupEnum(declName); ed != nil {
		return enumWirePrim(ed), declName
	}
	return "string", declName
}

func enumWirePrim(ed *ast.EnumDecl) string {
	if semantic.EnumKind(ed) == ast.EnumInt {
		return "int"
	}
	return "string"
}

// formatToString renders access as a string expression, converting a scalar or enum
// (declName != prim) to its primitive first; needsStrconv reports a strconv call.
func formatToString(prim, declName, access string) (expr string, needsStrconv bool) {
	named := declName != prim
	sp, ok := prims.Lookup(prim)
	if !ok {
		return access, false
	}
	switch sp.Kind {
	case prims.String:
		if named {
			return "string(" + access + ")", false
		}
		return access, false
	case prims.Bool:
		if named {
			return "strconv.FormatBool(bool(" + access + "))", true
		}
		return "strconv.FormatBool(" + access + ")", true
	case prims.Int:
		if !named && sp.Bits == 0 {
			return "strconv.Itoa(" + access + ")", true
		}
		if !named && sp.Bits == 64 {
			return "strconv.FormatInt(" + access + ", 10)", true
		}
		return "strconv.FormatInt(int64(" + access + "), 10)", true
	case prims.Uint:
		if !named && sp.Bits == 64 {
			return "strconv.FormatUint(" + access + ", 10)", true
		}
		return "strconv.FormatUint(uint64(" + access + "), 10)", true
	case prims.Float:
		if !named && sp.Bits == 64 {
			return "strconv.FormatFloat(" + access + ", 'g', -1, 64)", true
		}
		return fmt.Sprintf("strconv.FormatFloat(float64(%s), 'g', -1, %d)", access, sp.Bits), true
	}
	return access, false
}

// collectFormBindings returns the [semantic.FormFields] parts, each text part with its bind statement.
func collectFormBindings(m *ast.Method, pkg *semantic.Package, pkgAlias string, r *projectResolver) (text, files []paramBinding, err error) {
	nText, nFiles := semantic.FormFields(m, pkg, r.Resolver, resolvedGoFieldNames)
	if len(nFiles) == 0 {
		return nil, nil, nil
	}
	for _, ff := range nFiles {
		files = append(files, paramBinding{
			DSLName:   ff.WireName,
			GoName:    ff.Name,
			Required:  ff.Required,
			Field:     ff.Field,
			IsArray:   ff.IsArray,
			MimeTypes: ff.MimeTypes,
		})
	}
	for _, ff := range nText {
		line, lerr := renderWireBindLine(ff.Field, pkg, r, pkgAlias, ff.WireName, ff.Name, formSource())
		if lerr != nil {
			return nil, nil, fmt.Errorf("%s.%s on %s %s: %w",
				m.Request.Name.String(), ff.Field.Name, httpVerb(m.Verb), route.PathString(m.Path), lerr)
		}
		text = append(text, paramBinding{
			DSLName:  ff.WireName,
			GoName:   ff.Name,
			Required: ff.Required,
			Field:    ff.Field,
			Bind:     line,
		})
	}
	return text, files, nil
}

// collectBindings renders the path, query, header and cookie bindings of m's request fields,
// failing on a field its binding source cannot carry.
func collectBindings(m *ast.Method, pkg *semantic.Package, pkgAlias string, r *projectResolver) (path, query, header, cookie []paramBinding, err error) {
	if m.Request == nil {
		return
	}
	reqName := m.Request.Name.String()
	for _, rf := range resolveRequestFields(m, pkg, r) {
		// A @sensitive field is never read from the wire.
		if rf.Binding == wire.BindSensitive {
			continue
		}
		f := rf.Field
		wireName := rf.WireName()
		switch rf.Binding {
		case wire.BindPath:
			// A field that cannot bind as a segment is an error under @path and skipped when auto-bound.
			if f.Type != nil && (f.Type.Optional || f.Type.Array) {
				if rf.AutoBound {
					continue
				}
				err = fmt.Errorf("%s.%s: @path requires a non-optional, non-array field - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			line, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, rf.GoName, pathSource())
			if lerr != nil {
				if rf.AutoBound {
					continue
				}
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), route.PathString(m.Path), lerr)
				return
			}
			path = append(path, paramBinding{
				DSLName: wireName,
				GoName:  rf.GoName,
				Bind:    line,
			})
		case wire.BindQuery:
			line, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, rf.GoName, querySource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), route.PathString(m.Path), lerr)
				return
			}
			query = append(query, paramBinding{DSLName: wireName, GoName: rf.GoName, Bind: line})
		case wire.BindHeader:
			line, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, rf.GoName, headerSource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), route.PathString(m.Path), lerr)
				return
			}
			header = append(header, paramBinding{DSLName: wireName, GoName: rf.GoName, Bind: line})
		case wire.BindCookie:
			line, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, rf.GoName, cookieSource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), route.PathString(m.Path), lerr)
				return
			}
			cookie = append(cookie, paramBinding{DSLName: wireName, GoName: rf.GoName, Bind: line})
		}
	}
	return
}

// collectRequestFieldImports maps alias to import path for the packages that m's wire-bound
// request fields, and its cross-package @default pre-fills, reference.
func collectRequestFieldImports(m *ast.Method, pkg *semantic.Package, r *projectResolver) map[string]string {
	out := map[string]string{}
	if m == nil || m.Request == nil || pkg == nil || len(r.CrossPkg) == 0 {
		return out
	}
	if td, _ := semantic.LookupMethodType(m.Request, r.Resolver); td == nil {
		return out
	}
	set := map[string]bool{}
	addImports := r.CrossPkg.importsInto(set)
	for _, rf := range resolveRequestFields(m, pkg, r) {
		switch rf.Binding {
		case wire.BindPath, wire.BindQuery, wire.BindHeader, wire.BindCookie, wire.BindForm:
			rf.Field.Type.WalkNamedRefs(addImports)
		}
		// A pre-fill names the foreign package: `xshared.XColorRed`, `shared.Code("USD")`.
		if isQualifiedNamedWithDefault(rf.Field, r.CrossPkg) {
			rf.Field.Type.WalkNamedRefs(addImports)
		}
	}
	for pkgName, path := range r.CrossPkg {
		if !set[path] {
			continue
		}
		out[pkgName] = path
	}
	return out
}

// isQualifiedNamedWithDefault reports whether f is a cross-package `pkg.Name` field carrying @default.
func isQualifiedNamedWithDefault(f *ast.Field, crossPkg crossPkg) bool {
	if f == nil || f.Type == nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return false
	}
	parts := f.Type.Named.Name.Parts
	if len(parts) != 2 {
		return false
	}
	if _, ok := crossPkg[parts[0]]; !ok {
		return false
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != "default" || len(d.Args) != 1 {
			continue
		}
		return true
	}
	return false
}

// describeFieldType renders f's type for an error message, eliding type and map arguments
// (`[]Point`, `Page<...>?`, `map<...>`).
func describeFieldType(f *ast.Field) string {
	if f == nil || f.Type == nil {
		return "<unresolved>"
	}
	t := f.Type
	switch {
	case t.Map != nil:
		return "map<...>"
	case t.Named == nil:
		return "<anonymous>"
	}
	name := t.Named.Name.String()
	if len(t.Named.Args) > 0 {
		name += "<...>"
	}
	if t.Array {
		name = "[]" + name
	}
	if t.Optional {
		name += "?"
	}
	return name
}

// hasUnboundField reports whether any request field binds to the body or a form part.
func hasUnboundField(m *ast.Method, pkg *semantic.Package, r *projectResolver) bool {
	if m.Request == nil {
		return false
	}
	for _, rf := range resolveRequestFields(m, pkg, r) {
		switch rf.Binding {
		case wire.BindBody, wire.BindForm:
			return true
		}
	}
	return false
}
