// Transport: path/query/header/cookie/form/response binding collection.
package codegen

import (
	"fmt"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// flattenFields returns td's fields with embedded mixins expanded in
// declaration order: every `Mixin` member contributes the fields of the
// type it names (recursively), the same fields the JSON body schema
// (allOf $ref) and the validator (mixinValidateCall) already pull in. The
// wire-binding, OpenAPI-parameter, default pre-fill, and body-decode
// passes call this so a field a request inherits through a mixin is bound,
// documented, defaulted, and decoded — not silently dropped while the
// validator still enforces it. `r` may be nil (the OpenAPI pass runs on
// the merged single package, where pkg.Types already holds every type);
// `seen` breaks mixin cycles.
func flattenFields(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver, seen map[string]bool) []*ast.Field {
	if td == nil {
		return nil
	}
	var out []*ast.Field
	for _, m := range td.Body {
		switch v := m.(type) {
		case *ast.Field:
			out = append(out, v)
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			name := v.Ref.Name.String()
			if seen[name] {
				continue
			}
			seen[name] = true
			var mt *ast.TypeDecl
			if pkg != nil {
				mt = pkg.Types[name]
			}
			if mt == nil && r != nil {
				mt = r.LookupType(name)
			}
			sub := flattenFields(mt, pkg, r, seen)
			// A generic mixin (`Page<Item>`) promotes fields typed in the
			// type-parameter (`items T[]`). Substitute the concrete arguments
			// so every consumer — wire binder, OpenAPI params/body, default
			// pre-fill — sees `items Item[]`, not the bare `T`.
			if mt != nil && len(v.Ref.Args) > 0 && len(mt.TypeParams) > 0 {
				subst := map[string]*ast.TypeRef{}
				for i, p := range mt.TypeParams {
					if i < len(v.Ref.Args) {
						subst[p] = v.Ref.Args[i]
					}
				}
				for i, f := range sub {
					fc := *f
					fc.Type = substituteTypeRef(f.Type, subst)
					sub[i] = &fc
				}
			}
			out = append(out, sub...)
		}
	}
	return out
}

// requestFields is the mixin-aware field list of a request / response
// type: [flattenFields] with a fresh cycle-guard.
func requestFields(td *ast.TypeDecl, pkg *semantic.Package, r *ProjectResolver) []*ast.Field {
	return flattenFields(td, pkg, r, map[string]bool{})
}

// collectResponseBindings walks the response type's fields and renders
// the `@header` / `@cookie` writers. Each entry's [paramBinding.Bind]
// holds the fully-rendered Go statement (formatting handled per type),
// so the template only drops it verbatim. needsStrconv is true when any
// non-string value needs the strconv import. The resolver `r` resolves
// cross-package scalars / enums to their wire primitive.
func collectResponseBindings(m *ast.Method, pkg *semantic.Package, r *ProjectResolver) (headers, cookies []paramBinding, needsStrconv bool) {
	if m.Response == nil || m.Response.Type == nil {
		return nil, nil, false
	}
	td, ok := pkg.Types[m.Response.Type.Name.String()]
	if !ok {
		return nil, nil, false
	}
	// Flatten so a @header / @cookie field promoted through a mixin is
	// written on the response too — the OpenAPI doc side (binResponseFields)
	// already flattens, so without this the spec advertises a header the
	// handler never emits.
	for _, f := range requestFields(td, pkg, r) {
		kind := bindingFromDecorators(f.Decorators)
		if kind != "header" && kind != "cookie" {
			continue
		}
		stmt, ns := renderResponseWrite(f, pkg, r, kind, "resp")
		if ns {
			needsStrconv = true
		}
		entry := paramBinding{Bind: stmt}
		switch kind {
		case "header":
			headers = append(headers, entry)
		case "cookie":
			cookies = append(cookies, entry)
		}
	}
	return headers, cookies, needsStrconv
}

// renderResponseWrite builds the Go statement that writes field f onto
// the response as a `@header` or `@cookie`. accessVar names the struct
// the value is read from — `resp` for a normal response, `e` for an
// error. Non-string values are formatted via strconv (HTTP headers and
// cookies are string-valued on the wire); optional fields are
// nil-guarded; array headers emit one `Header().Add` per element
// (cookies are guaranteed non-array by the semantic layer). The
// returned statement may span several lines — gofmt, run over the whole
// generated file, normalises the indentation.
func renderResponseWrite(f *ast.Field, pkg *semantic.Package, r *ProjectResolver, kind, accessVar string) (stmt string, needsStrconv bool) {
	prim, declName := wirePrimName(f, pkg, r)
	wire := bindingWireName(f, kind)
	field := accessVar + "." + GoFieldName(f.Name)

	set := func(valueExpr string) string {
		if kind == "cookie" {
			return fmt.Sprintf("http.SetCookie(w, &http.Cookie{Name: %q, Value: %s})", wire, valueExpr)
		}
		return fmt.Sprintf("w.Header().Set(%q, %s)", wire, valueExpr)
	}

	switch {
	case f.Type != nil && f.Type.Array:
		// Header arrays write one line per element; @cookie arrays are
		// rejected at semantic time so this branch is header-only.
		expr, ns := formatToString(prim, declName, "_v")
		return fmt.Sprintf("for _, _v := range %s {\nw.Header().Add(%q, %s)\n}", field, wire, expr), ns
	case f.Type != nil && f.Type.Optional:
		// Optional is restricted to string-backed types by the wire
		// check, so the nil-guarded value is safe to deref + format.
		expr, ns := formatToString(prim, declName, "*"+field)
		return fmt.Sprintf("if %s != nil {\n%s\n}", field, set(expr)), ns
	default:
		expr, ns := formatToString(prim, declName, field)
		return set(expr), ns
	}
}

// wirePrimName resolves a field's declared type to the underlying wire
// primitive ("string", "bool", "int", "int64", "uint32", "float64",
// ...) used to format it onto a response header / cookie. It follows a
// local or cross-package scalar to its primitive and maps an enum to
// "int" (int-backed) or "string" (bare / string-backed). declName is
// the field's own type name — it differs from prim for scalars and
// enums and drives the Go conversion in [formatToString]. An
// unresolvable type (a cross-package symbol with no resolver) falls
// back to "string": the field already passed the wire-binding check, so
// it wraps some string/number/bool, and a wrong guess surfaces as a
// compile error rather than a silent drop.
func wirePrimName(f *ast.Field, pkg *semantic.Package, r *ProjectResolver) (prim, declName string) {
	if f.Type == nil || f.Type.Named == nil {
		return "string", ""
	}
	declName = f.Type.Named.Name.String()
	if idents.IsWireParseable(declName) {
		return declName, declName
	}
	if pkg != nil {
		if sc, ok := pkg.Scalars[declName]; ok && sc != nil {
			if idents.IsWireParseable(sc.Primitive) {
				return sc.Primitive, declName
			}
		}
		if ed, ok := pkg.Enums[declName]; ok && ed != nil {
			return enumWirePrim(ed), declName
		}
	}
	if r != nil {
		if sc := r.LookupScalar(declName); sc != nil {
			if idents.IsWireParseable(sc.Primitive) {
				return sc.Primitive, declName
			}
		}
		if ed := r.LookupEnum(declName); ed != nil {
			return enumWirePrim(ed), declName
		}
	}
	return "string", declName
}

// enumWirePrim maps an enum to the wire primitive its values serialise
// as: int-backed enums format as integers, bare / string-backed enums
// as their string value.
func enumWirePrim(ed *ast.EnumDecl) string {
	if firstEnumKind(ed) == ast.EnumInt {
		return "int"
	}
	return "string"
}

// formatToString returns the Go expression that renders `access` (a
// value of the field's declared type) as the string written onto a
// header / cookie, plus whether it needs the strconv import. Plain
// strings pass through untouched; every other primitive goes through
// strconv with the minimal conversion for its width. `named` marks a
// scalar / enum wrapping the primitive, which needs an explicit
// conversion the bare primitive does not.
func formatToString(prim, declName, access string) (expr string, needsStrconv bool) {
	named := declName != prim
	switch prim {
	case "string":
		if named {
			return "string(" + access + ")", false
		}
		return access, false
	case "bool":
		if named {
			return "strconv.FormatBool(bool(" + access + "))", true
		}
		return "strconv.FormatBool(" + access + ")", true
	case "int":
		if named {
			return "strconv.FormatInt(int64(" + access + "), 10)", true
		}
		return "strconv.Itoa(" + access + ")", true
	case "int8", "int16", "int32":
		return "strconv.FormatInt(int64(" + access + "), 10)", true
	case "int64":
		if named {
			return "strconv.FormatInt(int64(" + access + "), 10)", true
		}
		return "strconv.FormatInt(" + access + ", 10)", true
	case "uint", "uint8", "uint16", "uint32":
		return "strconv.FormatUint(uint64(" + access + "), 10)", true
	case "uint64":
		if named {
			return "strconv.FormatUint(uint64(" + access + "), 10)", true
		}
		return "strconv.FormatUint(" + access + ", 10)", true
	case "float32":
		return "strconv.FormatFloat(float64(" + access + "), 'g', -1, 32)", true
	case "float64":
		if named {
			return "strconv.FormatFloat(float64(" + access + "), 'g', -1, 64)", true
		}
		return "strconv.FormatFloat(" + access + ", 'g', -1, 64)", true
	}
	return access, false
}

// hasPassthroughDecorator reports whether `@passthrough` is declared
// on the method. Passthrough methods bypass the framework entirely:
// codegen emits a thin `http.HandlerFunc` that delegates to logic
// without parsing, validating, or encoding anything.

// collectFormBindings walks the request type's fields and partitions
// them into the multipart binder's two buckets: text fields (rendered
// via [renderWireBindLine] with a [formSource]) and file fields
// (`*multipart.FileHeader`, bound via r.FormFile in the template).
//
// The function ONLY produces text bindings when at least one file
// field is present - without a file, the request has no multipart
// handler to feed and the would-be text fields go through the JSON
// body decoder via standard struct semantics instead.
//
// `needsStrconv` is true when any text field's binding line reaches
// into the strconv package - flows through to the multipart template
// import block.
func collectFormBindings(m *ast.Method, pkg *semantic.Package, pkgAlias string, r *ProjectResolver) (text, files []paramBinding, needsStrconv bool, err error) {
	if m.Request == nil {
		return nil, nil, false, nil
	}
	// First pass: find file fields. Without one, the handler renders
	// as a plain JSON body decoder and we have nothing to emit here.
	type candidate struct {
		field *ast.Field
		entry paramBinding
	}
	var nonFile []candidate
	// Read the resolved IR (mixins flattened, auto-@path resolved): a form
	// field is one that rides the request body — body or @form — and is not
	// a wire param or a server-only @sensitive field. Skipping @sensitive
	// here also keeps such a value out of the multipart binding, matching
	// the JSON binder.
	for _, rf := range resolveRequestFields(m, pkg, r) {
		switch rf.Binding {
		case BindPath, BindQuery, BindHeader, BindCookie, BindSensitive:
			continue
		}
		f := rf.Field
		// Wire name honours an explicit `@form("field_name")` arg (same
		// rule as the path/query/header/cookie binders via bindingWireName)
		// so the generated r.FormFile / r.FormValue key and the multipart
		// schema property name both match what the client sends, rather
		// than falling back to the Go field name (`@form("avatar_file")`
		// binds to `avatar_file`, not `avatarFile`).
		entry := paramBinding{DSLName: bindingWireName(f, "form"), GoName: GoFieldName(f.Name), Required: fieldIsRequired(f), Field: f}
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "file" {
			for _, d := range f.Decorators {
				if d == nil || d.Name != "mimeTypes" || len(d.Args) == 0 {
					continue
				}
				if mimes, ok := stringArrayArg(d.Args[0]); ok {
					entry.MimeTypes = mimes
				} else {
					for _, a := range d.Args {
						if s, ok := a.Value.(*ast.StringLit); ok {
							entry.MimeTypes = append(entry.MimeTypes, s.Value)
						}
					}
				}
			}
			files = append(files, entry)
			continue
		}
		nonFile = append(nonFile, candidate{field: f, entry: entry})
	}
	if len(files) == 0 {
		// No multipart handler will be emitted; surrender the
		// non-file fields back to the JSON body path.
		return nil, nil, false, nil
	}
	// Second pass: render bindings for the text fields now that we
	// know the handler is multipart.
	for _, c := range nonFile {
		line, needs, lerr := renderWireBindLine(c.field, pkg, r, pkgAlias, bindingWireName(c.field, "form"), formSource())
		if lerr != nil {
			err = fmt.Errorf("%s.%s on %s %s: %w", m.Request.Name.String(), c.field.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
			return
		}
		if needs {
			needsStrconv = true
		}
		c.entry.Bind = line
		text = append(text, c.entry)
	}
	return text, files, needsStrconv, nil
}

// collectBindings walks the request type's fields and returns per-kind
// binding tables (path, query, header, cookie) plus a flag noting
// whether any emitted block reaches into `strconv`.
//
// Resolution order for a field's bucket:
//  1. Explicit `@path` / `@query` / `@header` / `@cookie` decorator wins.
//  2. A name match against a `{param}` segment in the method path
//     promotes the field to `path` (so `id string` on `/users/{id}`
//     auto-binds without a decorator).
//  3. For non-body verbs (GET / DELETE / HEAD / OPTIONS) any leftover
//     unbound field defaults to `query` - the README's "Default
//     binding by verb" rule.
//
// Path / header / cookie require string-typed fields (URLs and HTTP
// headers carry strings on the wire). Query supports the full
// numeric / float / bool / array matrix; the per-field [Bind] is
// pre-rendered Go that the handler template emits verbatim.
//
// Unsupported binding shapes (struct/[]struct/map on @query, non-string
// on @path/@header/@cookie) return a non-nil error so the misuse is
// flagged at `craftgo gen` time rather than skipped.
func collectBindings(m *ast.Method, pkg *semantic.Package, pkgAlias string, r *ProjectResolver) (path, query, header, cookie []paramBinding, needsStrconv bool, err error) {
	if m.Request == nil {
		return
	}
	reqName := m.Request.Name.String()
	// Read the resolved IR: the full binding (explicit + auto-@path/@query,
	// mixins flattened, cross-package request type resolved) is computed
	// once in resolveRequestFields, so the binder's view can't drift from
	// the OpenAPI parameter categorisation.
	for _, rf := range resolveRequestFields(m, pkg, r) {
		// @sensitive fields never cross the wire (json:"-", excluded from the
		// OpenAPI schema): the binder must not read them from any source, or
		// an un-decorated sensitive field would auto-promote to @query and
		// leak into the URL while the spec documents no such parameter.
		if rf.Binding == BindSensitive {
			continue
		}
		f := rf.Field
		// Wire-side name honours an explicit decorator arg
		// (`@path("user_id")` binds segment `{user_id}` even when the Go
		// field is `UserId`).
		wireName := rf.WireName()
		switch rf.Binding {
		case BindPath:
			// A path segment binds like a @query value — a string passes
			// straight through, a numeric / scalar / enum parses via the
			// same server.Parse* helper — but it is always present and
			// single-valued, so renderWireBindLine emits the required
			// directSingle / singleParsed shape. An optional or array
			// @path field is rejected (the semantic layer reports it for an
			// explicit @path); under auto-promotion a non-bindable body
			// field that merely shares a segment name is skipped silently.
			if f.Type != nil && (f.Type.Optional || f.Type.Array) {
				if rf.AutoBound {
					continue
				}
				err = fmt.Errorf("%s.%s: @path requires a non-optional, non-array field - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			line, _, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, pathSource())
			if lerr != nil {
				if rf.AutoBound {
					continue
				}
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			path = append(path, paramBinding{
				DSLName: wireName,
				GoName:  GoFieldName(f.Name),
				Bind:    line,
			})
		case BindQuery:
			line, needs, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, querySource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			query = append(query, paramBinding{DSLName: wireName, GoName: GoFieldName(f.Name), Bind: line})
		case BindHeader:
			line, needs, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, headerSource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			header = append(header, paramBinding{DSLName: wireName, GoName: GoFieldName(f.Name), Bind: line})
		case BindCookie:
			line, needs, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, cookieSource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			cookie = append(cookie, paramBinding{DSLName: wireName, GoName: GoFieldName(f.Name), Bind: line})
		}
	}
	return
}

// collectRequestFieldImports walks every WIRE-BOUND field of the
// method's request type (path / query / header / cookie, explicit or
// auto-promoted) and returns the Go imports their types reach into.
// Body fields are excluded - the JSON decoder reads them through the
// request struct's own package and pulling that import in here would
// surface as an unused-import build failure.
//
// Result keys the DSL package name (used as the Go alias in the
// binder cast) to its full Go import path, ready to append to the
// handler's extra-imports block.
func collectRequestFieldImports(m *ast.Method, pkg *semantic.Package, crossPkg CrossPkg) map[string]string {
	out := map[string]string{}
	if m == nil || m.Request == nil || pkg == nil || len(crossPkg) == 0 {
		return out
	}
	if _, ok := pkg.Types[m.Request.Name.String()]; !ok {
		return out
	}
	set := map[string]bool{}
	// Read the resolved IR: the wire-bound classification (explicit +
	// auto-@path/@query, mixins flattened) is computed once in
	// resolveRequestFields rather than re-derived here.
	for _, rf := range resolveRequestFields(m, pkg, nil) {
		switch rf.Binding {
		case BindPath, BindQuery, BindHeader, BindCookie:
			walkCrossPkgImports(rf.Field.Type, crossPkg, set)
		}
		// Body field with `@default(...)` on a cross-pkg enum OR scalar
		// emits a pre-fill line that references the foreign package and
		// so needs its import. Enum: `__d := xshared.XColorRed`. Scalar:
		// `__d := shared.CurrencyCode("USD")` — the literal is CAST to
		// the scalar's defined Go type (scalars are defined types, not
		// aliases), so the cast references the foreign package and the
		// import is required. The trigger is "field type is a cross-pkg
		// named ref AND has @default".
		if isQualifiedNamedWithDefault(rf.Field, crossPkg) {
			walkCrossPkgImports(rf.Field.Type, crossPkg, set)
		}
	}
	for pkgName, path := range crossPkg {
		if !set[path] {
			continue
		}
		out[pkgName] = path
	}
	return out
}

// isQualifiedNamedWithDefault reports whether f's type is a qualified
// `pkg.Name` ref AND the field carries a `@default(...)` decorator.
// Sufficient signal that the transport pre-fill will emit a
// foreign-package reference — either an enum const (`pkg.NameValue`)
// or a scalar cast (`pkg.Name(literal)`) — so the cross-pkg import is
// registered. A false-positive registration is harmless: the
// import-block emitter dedups against actual usage, and a genuinely
// unused entry would surface as an `unused-import` build failure (the
// renderDefault layer only emits a qualified reference when the arg
// resolves to an enum const or a scalar cast).
func isQualifiedNamedWithDefault(f *ast.Field, crossPkg CrossPkg) bool {
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

// bindingWireName returns the wire-side parameter name for a bound
// field. The default is the DSL field name; an explicit string
// argument on the binding decorator (`@path("user_id")`,
// `@header("X-API-Key")`, etc.) overrides it so wire-side conventions
// (snake_case path segments, kebab/hyphen HTTP headers) can differ
// from the Go field name. `kind` selects which decorator to inspect
// (`path`/`query`/`header`/`cookie`) so the same field may carry the
// wrong-decorator's arg without leakage. The rule lives in
// [semantic.WireName] so the analyser's binding checks and these binders
// agree on the emitted name.
func bindingWireName(f *ast.Field, kind string) string {
	return semantic.WireName(f, kind)
}

// describeFieldType renders a short human-readable form of f's type
// for error messages — `[]Point`, `Page<Book>`, `map<string,int>`,
// etc. Used by the binding-rejection paths so the user sees the exact
// shape that violated the binding contract.
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

// hasUnboundField reports whether the request type has at least one
// field that does NOT carry an explicit @path/@query/@header/@cookie
// (or @body/@form) decorator and is not implicitly path-bound. The
// handler only emits the JSON body decode block when one or more body
// fields exist, so a request whose every field is parameter-bound
// skips the decode entirely.
func hasUnboundField(m *ast.Method, pkg *semantic.Package) bool {
	if m.Request == nil {
		return false
	}
	// A field rides the body iff the resolved binding is body or form (an
	// explicit @body / @form, or an un-decorated field that auto-bound to
	// @body on this verb). Wire params (@path/@query/@header/@cookie, incl.
	// auto-@path) do not.
	for _, rf := range resolveRequestFields(m, pkg, nil) {
		switch rf.Binding {
		case BindBody, BindForm:
			return true
		}
	}
	return false
}
