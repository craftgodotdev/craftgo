// Transport: path/query/header/cookie/form/response binding collection + string-binding helpers.
package codegen

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func collectResponseBindings(m *ast.Method, pkg *semantic.Package) (headers, cookies []paramBinding) {
	if m.Response == nil || m.Response.Type == nil {
		return nil, nil
	}
	td, ok := pkg.Types[m.Response.Type.Name.String()]
	if !ok {
		return nil, nil
	}
	for _, member := range td.Body {
		f, ok := member.(*ast.Field)
		if !ok {
			continue
		}
		if !isPlainStringField(f) {
			continue
		}
		// HTTP header / cookie names live in a different character
		// set than DSL field names (hyphens, mixed case): `apiKey
		// string @header("X-API-Key")` declares a Go field `ApiKey`
		// that must reach the wire as the canonical `X-API-Key`.
		// bindingWireName returns the explicit decorator arg when
		// present and falls back to the field name otherwise.
		kind := bindingFromDecorators(f.Decorators)
		entry := paramBinding{
			DSLName: bindingWireName(f, kind),
			GoName:  GoFieldName(f.Name),
		}
		switch kind {
		case "header":
			headers = append(headers, entry)
		case "cookie":
			cookies = append(cookies, entry)
		}
	}
	return headers, cookies
}

// hasPassthroughDecorator reports whether `@passthrough` is declared
// on the method. Passthrough methods bypass the framework entirely:
// codegen emits a thin `http.HandlerFunc` that delegates to logic
// without parsing, validating, or encoding anything.

func collectFormBindings(m *ast.Method, pkg *semantic.Package) (strings, files []paramBinding) {
	if m.Request == nil {
		return nil, nil
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return nil, nil
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
		}
		if pathSegs[f.Name] {
			continue
		}
		entry := paramBinding{DSLName: f.Name, GoName: GoFieldName(f.Name)}
		if f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "file" {
			// Record the @mimeTypes allowlist (if any) so the
			// OpenAPI multipart emitter can render
			// `encoding[field].contentType` — without this the
			// client SDK has no way to see what MIME types the
			// server's runtime validator will accept.
			for _, d := range f.Decorators {
				if d == nil || d.Name != "mimeTypes" || len(d.Args) == 0 {
					continue
				}
				// Canonical syntax is variadic — `@mimeTypes("a",
				// "b")`. The legacy array form `@mimeTypes(["a",
				// "b"])` still parses (registry sets
				// AllowArrayShortcut for back-compat); try the
				// array branch first, fall back to collecting
				// each positional string arg.
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
		if isPlainStringField(f) {
			strings = append(strings, entry)
		}
	}
	return strings, files
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
//     binding theo verb" rule wired up at last.
//
// Path / header / cookie still require string-typed fields (URLs and
// HTTP headers carry strings on the wire). Query supports the full
// numeric / float / bool / array matrix; the per-field [Bind] is
// pre-rendered Go that the handler template emits verbatim.
//
// Unsupported binding shapes (struct/[]struct/map on @query, non-string
// on @path/@header/@cookie) return a non-nil error. Silent skips were
// removed in favour of fail-fast feedback at `craftgo gen` time.
func collectBindings(m *ast.Method, pkg *semantic.Package, pkgAlias string, scalars ScalarTable) (path, query, header, cookie []paramBinding, needsStrconv bool, err error) {
	if m.Request == nil {
		return
	}
	td, ok := pkg.Types[m.Request.Name.String()]
	if !ok {
		return
	}
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
			if !stringBindable(f, pkg, scalars) {
				if auto {
					// Auto-promoted from a path segment match - silently skip
					// so a body field that happens to share a name with a
					// segment doesn't break the build. Explicit @path is
					// strict (handled above by entering this case via the
					// decorator scan); auto-promotion is permissive.
					continue
				}
				err = fmt.Errorf("%s.%s: @path requires a string-backed field (string, string scalar, or string enum) - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			path = append(path, paramBinding{
				DSLName: wireName,
				GoName:  GoFieldName(f.Name),
				Bind:    fmt.Sprintf("req.%s = %s", GoFieldName(f.Name), stringBindCast(f, fmt.Sprintf("r.PathValue(%q)", wireName), pkgAlias)),
			})
		case "query":
			line, needs, lerr := renderQueryBindLine(f, pkg, pkgAlias, wireName)
			if lerr != nil {
				err = fmt.Errorf("%s.%s on %s %s: %w", reqName, f.Name, httpVerb(m.Verb), pathString(m.Path), lerr)
				return
			}
			if needs {
				needsStrconv = true
			}
			query = append(query, paramBinding{
				DSLName: wireName,
				GoName:  GoFieldName(f.Name),
				Bind:    line,
			})
		case "header":
			if !stringBindable(f, pkg, scalars) {
				err = fmt.Errorf("%s.%s: @header requires a string-backed field (string, string scalar, or string enum) - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			header = append(header, paramBinding{
				DSLName: wireName,
				GoName:  GoFieldName(f.Name),
				Bind:    fmt.Sprintf("req.%s = %s", GoFieldName(f.Name), stringBindCast(f, fmt.Sprintf("r.Header.Get(%q)", wireName), pkgAlias)),
			})
		case "cookie":
			if !stringBindable(f, pkg, scalars) {
				err = fmt.Errorf("%s.%s: @cookie requires a string-backed field (string, string scalar, or string enum) - got %s", reqName, f.Name, describeFieldType(f))
				return
			}
			cookie = append(cookie, paramBinding{
				DSLName: wireName,
				GoName:  GoFieldName(f.Name),
				Bind: fmt.Sprintf(`if c, err := r.Cookie(%q); err == nil {
	req.%s = %s
}`, wireName, GoFieldName(f.Name), stringBindCast(f, "c.Value", pkgAlias)),
			})
		}
	}
	return
}

// pathString re-renders a method's path for error messages
// (`/books/{id}/cancel`); empty string when m has no path block.
//
// Hot path (called per method during routes-go emission): Builder
// keeps the per-segment append allocation-free.

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
	}
	for pkgName, path := range crossPkg {
		if !set[path] {
			continue
		}
		out[pkgName] = path
	}
	return out
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

// renderQueryBindLine returns the Go source that binds one field from
// the URL query string. Shape varies by field type:
//   - string single → `req.X = r.URL.Query().Get("x")`
//   - numeric/bool single → `if v := ...; v != "" { parse + cast }`
//   - []string → `req.X = r.URL.Query()["x"]`
//   - []numeric/bool → `for ... { parse + append }`
//   - scalar X / enum X → resolved to underlying primitive then cast
//     (string-backed scalar/enum: `req.X = X(r.URL.Query().Get("x"))`;
//     int-backed: parse int then `X(_n)`).
//
// `wireName` is the on-the-wire query parameter key — either the DSL
// field name (default) or the explicit override from `@query("name")`.
// Returns a non-nil error for unsupported field shapes (structs,
// []struct, maps, generics, ...) so the codegen surfaces the
// mistake at `craftgo gen` time instead of silently producing a
// handler that leaves the field zero-valued. The second return
// value is true when the rendered code references "strconv" so
// the caller can flip the import flag once.

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

func isPlainStringField(f *ast.Field) bool {
	if f.Type == nil || f.Type.Array || f.Type.Optional {
		return false
	}
	return f.Type.Named != nil && f.Type.Named.Name.String() == "string"
}

// stringBindable reports whether f's type can ride a path / header /
// cookie wire (always a string at the protocol level). Matches:
//   - the bare `string` primitive
//   - a `scalar X string @...` declared in any reachable package
//   - a string-backed enum (kind EnumBare or EnumString) in pkg
//
// `scalars` is the project-wide lookup table built by
// [BuildScalarTable]; it carries both local scalars (keyed by bare
// name) and cross-package scalars (keyed by qualified name like
// "shared.ID"). Without consulting it, every cross-package scalar
// binding was rejected — a `shared.ID @path` field hit the catch-all
// "@path requires a string-backed field" error even though
// `shared.ID` IS a string scalar.
//
// Mirrors `semantic.isStringBindingType` so design-time and gen-time
// rejections agree.
func stringBindable(f *ast.Field, pkg *semantic.Package, scalars ScalarTable) bool {
	if f == nil || f.Type == nil || f.Type.Array || f.Type.Optional || f.Type.Named == nil {
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
