// Transport: path/query/header/cookie/form/response binding collection + string-binding helpers.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

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
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
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
	if _, ok := queryPrims[declName]; ok {
		return declName, declName
	}
	if pkg != nil {
		if sc, ok := pkg.Scalars[declName]; ok && sc != nil {
			if _, ok := queryPrims[sc.Primitive]; ok {
				return sc.Primitive, declName
			}
		}
		if ed, ok := pkg.Enums[declName]; ok && ed != nil {
			return enumWirePrim(ed), declName
		}
	}
	if r != nil {
		if sc := r.LookupScalar(declName); sc != nil {
			if _, ok := queryPrims[sc.Primitive]; ok {
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
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return nil, nil, false, nil
	}
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	// First pass: find file fields. Without one, the handler renders
	// as a plain JSON body decoder and we have nothing to emit here.
	type candidate struct {
		field *ast.Field
		entry paramBinding
	}
	var nonFile []candidate
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		switch bindingFromDecorators(f.Decorators) {
		case "path", "query", "header", "cookie":
			continue
		}
		if pathSegs[f.Name] {
			continue
		}
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
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		// Cross-package request type (`request shared.Cred`) doesn't
		// land in pkg.Types — fall through the resolver so the
		// handler still binds its fields.
		if td2 := r.LookupType(m.Request.Name.String()); td2 != nil {
			td = td2
		} else {
			return
		}
	}
	scalars := r.scalars()
	reqName := m.Request.Name.String()
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	autoQuery := !hasBodyVerb(m.Verb)
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		bind := bindingFromDecorators(f.Decorators)
		auto := false
		if bind == "" && pathSegs[f.Name] {
			bind = "path"
			auto = true
		}
		if bind == "" && autoQuery {
			bind = "query"
			auto = true
		}
		// Wire-side name defaults to the DSL field name, but an
		// explicit string argument on the binding decorator overrides
		// it: `@path("user_id")` binds the field to the path segment
		// `{user_id}` even when the Go field name is `UserId`. Without
		// honouring the arg, URLs like `/users/{user_id}` never match
		// the field `userId` because `r.PathValue("userId")` returns
		// the empty string.
		wireName := bindingWireName(f, bind)
		switch bind {
		case "path":
			if !stringBindable(f, pkg, scalars, false) {
				if auto {
					// Auto-promoted from a path segment match - silently skip
					// so a body field that happens to share a name with a
					// segment doesn't break the build. Explicit @path is
					// strict (handled above by entering this case via the
					// decorator scan); auto-promotion is permissive.
					continue
				}
				err = fmt.Errorf("%s.%s: @path requires a non-optional string-backed field (string, string scalar, or string enum) - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			path = append(path, paramBinding{
				DSLName: wireName,
				GoName:  GoFieldName(f.Name),
				Bind:    fmt.Sprintf("req.%s = %s", GoFieldName(f.Name), stringBindCast(f, fmt.Sprintf("r.PathValue(%q)", wireName), pkgAlias)),
			})
		case "query":
			line, needs, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, querySource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			query = append(query, paramBinding{DSLName: wireName, GoName: GoFieldName(f.Name), Bind: line})
		case "header":
			line, needs, lerr := renderWireBindLine(f, pkg, r, pkgAlias, wireName, headerSource())
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			header = append(header, paramBinding{DSLName: wireName, GoName: GoFieldName(f.Name), Bind: line})
		case "cookie":
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
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return out
	}
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	autoQuery := !hasBodyVerb(m.Verb)
	set := map[string]bool{}
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		bind := bindingFromDecorators(f.Decorators)
		if bind == "" && pathSegs[f.Name] {
			bind = "path"
		}
		if bind == "" && autoQuery {
			bind = "query"
		}
		switch bind {
		case "path", "query", "header", "cookie":
			walkCrossPkgImports(f.Type, crossPkg, set)
		}
		// Body field with `@default(...)` on a cross-pkg enum OR scalar
		// emits a pre-fill line that references the foreign package and
		// so needs its import. Enum: `__d := xshared.XColorRed`. Scalar:
		// `__d := shared.CurrencyCode("USD")` — the literal is CAST to
		// the scalar's defined Go type (scalars are defined types, not
		// aliases), so the cast references the foreign package and the
		// import is required. The trigger is "field type is a cross-pkg
		// named ref AND has @default".
		if isQualifiedNamedWithDefault(f, crossPkg) {
			walkCrossPkgImports(f.Type, crossPkg, set)
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
// wrong-decorator's arg without leakage.
func bindingWireName(f *ast.Field, kind string) string {
	if f == nil {
		return ""
	}
	for _, d := range f.Decorators {
		if d == nil || d.Name != kind || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok && s.Value != "" {
			return s.Value
		}
	}
	return f.Name
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

// parseCall renders the strconv.ParseX(_v, ...) expression for a
// numeric / bool primitive. Bool ignores bits; ParseFloat takes only
// (s, bits); ParseInt / ParseUint take (s, base, bits).

// stringBindable reports whether f's type can ride a path / header /
// cookie wire (always a string at the protocol level). Matches:
//   - the bare `string` primitive
//   - a `scalar X string @...` declared in any reachable package
//   - a string-backed enum (kind EnumBare or EnumString) in pkg
//
// Optional is accepted when allowOptional=true (header / cookie callers
// pass true; path callers pass false because path segments are
// mandatory by route-matching). Array / map shapes are always rejected
// regardless.
//
// `scalars` is the project-wide lookup table built by
// [BuildScalarTable]; it carries both local scalars (keyed by bare
// name) and cross-package scalars (keyed by qualified name like
// "shared.ID"). Consulting it lets a cross-package scalar binding
// (`shared.ID @path`, where `shared.ID` is a string scalar) pass the
// string-backed check.
//
// Mirrors `semantic.isStringBindingType` so design-time and gen-time
// rejections agree.
func stringBindable(f *ast.Field, pkg *semantic.Package, scalars ScalarTable, allowOptional bool) bool {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Named == nil {
		return false
	}
	if f.Type.Optional && !allowOptional {
		return false
	}
	name := f.Type.Named.Name.String()
	if name == "string" {
		return true
	}
	if sc := lookupScalar(name, pkg, scalars); sc != nil && sc.Primitive == "string" {
		return true
	}
	if pkg != nil {
		if ed, ok := pkg.Enums[name]; ok && ed != nil {
			k := firstEnumKind(ed)
			return k == ast.EnumBare || k == ast.EnumString
		}
	}
	return false
}

// lookupScalar resolves a possibly-qualified scalar name to its
// declaration, consulting the project-wide [ScalarTable] first
// (covers cross-package references) and falling back to the local
// `pkg.Scalars` map for legacy single-package callers that pass a
// nil table.
func lookupScalar(name string, pkg *semantic.Package, scalars ScalarTable) *ast.ScalarDecl {
	if scalars != nil {
		if sc, ok := scalars[name]; ok {
			return sc
		}
	}
	if pkg != nil {
		if sc, ok := pkg.Scalars[name]; ok {
			return sc
		}
	}
	return nil
}

// stringBindCast wraps `src` (a Go expression yielding a string) in
// the cast required to land in f's declared Go type. Plain `string`
// returns src unchanged; scalar or enum types wrap as
// `pkgAlias.TypeName(src)` so the cast resolves to the typed alias
// the request struct expects. Caller is responsible for confirming
// [stringBindable] first.
//
// pkgAlias is the Go-side import alias the request type lives under
// (typically "types" for local declarations) - the same alias used to
// qualify the request struct in the handler. Empty alias falls back
// to an unqualified `TypeName(...)`.
func stringBindCast(f *ast.Field, src, pkgAlias string) string {
	if f == nil || f.Type == nil || f.Type.Named == nil {
		return src
	}
	name := f.Type.Named.Name.String()
	if name == "string" {
		return src
	}
	// Cross-package references (`shared.ID`) carry their own
	// qualifier from the DSL and already match the Go import added
	// by the type resolver. Prepending the local `pkgAlias` would
	// produce `types.shared.ID(...)` which doesn't resolve — the
	// cross-pkg path skips the local alias entirely.
	if !strings.Contains(name, ".") && pkgAlias != "" {
		name = pkgAlias + "." + name
	}
	return name + "(" + src + ")"
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
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return false
	}
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		switch bindingFromDecorators(f.Decorators) {
		case "path", "query", "header", "cookie":
			continue
		case "body", "form":
			return true
		}
		// Implicit path match short-circuits - that's a path field, not body.
		if pathSegs[f.Name] {
			continue
		}
		return true
	}
	return false
}
