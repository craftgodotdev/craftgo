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
	for _, rf := range semantic.ResolveFields(td, prefix, pkg, r.Resolver, resolvedGoFieldNames) {
		kind, _ := wire.BindingKind(rf.Field.Decorators)
		if kind != wire.BindHeader && kind != wire.BindCookie {
			continue
		}
		stmt, ns := renderResponseWrite(rf, pkg, r, kind, accessVar)
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

// renderResponseWrite renders the statement writing accessVar's rf as a header or cookie: an
// optional field is nil-guarded and an array header adds one value per element.
func renderResponseWrite(rf semantic.ResolvedField, pkg *semantic.Package, r *projectResolver, kind wire.Binding, accessVar string) (stmt string, needsStrconv bool) {
	f := rf.Field
	prim, declared, ok := wireTarget(rf, pkg, r)
	if !ok {
		prim = "string"
	}
	named := declared != "" || !ok
	wireName := wire.WireName(f, kind)
	field := accessVar + "." + rf.Name

	set := func(valueExpr string) string {
		if kind == wire.BindCookie {
			return fmt.Sprintf("http.SetCookie(w, &http.Cookie{Name: %q, Value: %s})", wireName, valueExpr)
		}
		return fmt.Sprintf("w.Header().Set(%q, %s)", wireName, valueExpr)
	}

	switch {
	case f.Type != nil && f.Type.Array:
		// The analyser rejects a @cookie array, so this is a header.
		expr, ns := formatToString(prim, named, "_v")
		return fmt.Sprintf("for _, _v := range %s {\nw.Header().Add(%q, %s)\n}", field, wireName, expr), ns
	case f.Type != nil && f.Type.Optional:
		expr, ns := formatToString(prim, named, "*"+field)
		return fmt.Sprintf("if %s != nil {\n%s\n}", field, set(expr)), ns
	default:
		expr, ns := formatToString(prim, named, field)
		return set(expr), ns
	}
}

// formatToString renders access, of primitive prim or of a scalar or enum over it (named), as a
// string expression; needsStrconv reports a strconv call.
func formatToString(prim string, named bool, access string) (expr string, needsStrconv bool) {
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

// collectFormBindings returns the multipart parts of m's request fields, each text part with its
// bind statement; both are nil when no part is a file.
func collectFormBindings(m *ast.Method, fields []resolvedField, pkg *semantic.Package, r *projectResolver, imports *importSet) (text, files []paramBinding, err error) {
	resolved := make([]semantic.ResolvedField, len(fields))
	byField := make(map[*ast.Field]resolvedField, len(fields))
	for i, rf := range fields {
		resolved[i] = rf.ResolvedField
		byField[rf.Field] = rf
	}
	textParts, fileParts := semantic.FormParts(resolved)
	for _, ff := range fileParts {
		files = append(files, paramBinding{
			DSLName:   ff.WireName,
			GoName:    ff.Name,
			Required:  ff.Required,
			Field:     ff.Field,
			IsArray:   ff.IsArray,
			MimeTypes: ff.MimeTypes,
		})
	}
	for _, ff := range textParts {
		line, lerr := renderWireBindLine(byField[ff.Field], wire.BindForm, ff.WireName, pkg, r, imports)
		if lerr != nil {
			return nil, nil, bindError(m, ff.Field, lerr)
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

// collectBindings renders the bind statement of each request field a path, query, header or cookie
// carries, grouped by binding in field order; a field its source cannot carry fails the method,
// unless its name alone bound it to the path.
func collectBindings(m *ast.Method, fields []resolvedField, pkg *semantic.Package, r *projectResolver, imports *importSet) (map[wire.Binding][]paramBinding, error) {
	reqName := m.Request.Name.String()
	binds := map[wire.Binding][]paramBinding{}
	for _, rf := range fields {
		switch rf.Binding {
		case wire.BindPath, wire.BindQuery, wire.BindHeader, wire.BindCookie:
		default:
			continue
		}
		f := rf.Field
		autoPath := rf.Binding == wire.BindPath && rf.AutoBound
		// A route segment is one value and always present.
		if rf.Binding == wire.BindPath && f.Type != nil && (f.Type.Optional || f.Type.Array) {
			if autoPath {
				continue
			}
			return nil, fmt.Errorf("%s.%s: @path requires a non-optional, non-array field - got %s", reqName, f.Name, f.Type)
		}
		line, err := renderWireBindLine(rf, rf.Binding, rf.WireName(), pkg, r, imports)
		if err != nil {
			if autoPath {
				continue
			}
			return nil, bindError(m, f, err)
		}
		binds[rf.Binding] = append(binds[rf.Binding], paramBinding{DSLName: rf.WireName(), GoName: rf.GoName, Bind: line})
	}
	return binds, nil
}

// bindError names the request field of m that err keeps from binding.
func bindError(m *ast.Method, f *ast.Field, err error) error {
	return fmt.Errorf("%s.%s on %s %s: %w", m.Request.Name.String(), f.Name, httpVerb(m.Verb), route.PathString(m.Path), err)
}

// hasBodyField reports whether any request field binds to the body or a form part.
func hasBodyField(fields []resolvedField) bool {
	for _, rf := range fields {
		switch rf.Binding {
		case wire.BindBody, wire.BindForm:
			return true
		}
	}
	return false
}
