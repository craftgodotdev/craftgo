// Cross-decorator combination rules (defaults, bindings, single-binding, passthrough body) + ref walking helpers.
package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkQualifiedRefs() {
	for _, td := range a.pkg.Types {
		a.walkTypeMembers(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.walkTypeMembers(ed.Name, ed.Body)
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			if m.Request != nil {
				a.checkNamedRef("method "+m.Name+" request", m.Request)
			}
			if m.Response != nil && m.Response.Type != nil {
				a.checkNamedRef("method "+m.Name+" response", m.Response.Type)
			}
		}
	}
}

// walkTypeMembers checks every Field type reference in a type or error body
// for a qualified prefix. Mixin members are skipped (see [checkQualifiedRefs]).
func (a *analyzer) walkTypeMembers(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.walkTypeRef("field "+parent+"."+f.Name, f.Type)
	}
}

// walkTypeRef descends into a TypeRef and applies the qualified-name check
// to every NamedTypeRef encountered. Map keys, map values, and generic
// arguments are all visited recursively.
func (a *analyzer) walkTypeRef(scope string, t *ast.TypeRef) {
	if t == nil {
		return
	}
	if t.Map != nil {
		a.walkTypeRef(scope, t.Map.Key)
		a.walkTypeRef(scope, t.Map.Value)
		return
	}
	if t.Named != nil {
		a.checkNamedRef(scope, t.Named)
	}
}

// checkNamedRef reports a diagnostic when n.Name has more than one segment
// and recurses through n.Args so generic arguments are validated too.
func (a *analyzer) checkNamedRef(scope string, n *ast.NamedTypeRef) {
	if n == nil || n.Name == nil {
		return
	}
	if len(n.Name.Parts) > 1 {
		a.diag(n.Pos, n.Pos, lexer.SeverityError, CodeQualifiedRef,
			"cross-package qualified reference %q in %s is not supported (folder-merge model); use the unqualified name",
			n.Name.String(), scope)
	}
	for _, arg := range n.Args {
		a.walkTypeRef(scope, arg)
	}
}

// checkCombinationRules enforces the decorator-combination contract
// documented in the README §"Combination rules":
//
//   - At most one of `@path / @query / @header / @cookie / @body / @form`
//     may appear on a single field; multiple non-body bindings would
//     compete for the same value at runtime.
//   - `@passthrough` methods may not declare `request` or `response` -
//     logic handles the wire format directly, so any framework-managed
//     shape would be silently ignored.
//   - `@default` on a non-optional field surfaces a warning - the
//     formatter auto-adds `?` on save so the OpenAPI required[] no
//     longer contradicts the default's "fires when absent" intent.
//
// Diagnostics fire on the second / conflicting decorator so the error
// points at the offending source location, not the (innocent) first
// occurrence.
func (a *analyzer) checkCombinationRules(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclCombinations(d)
		}
	}
}

// checkDeclCombinations dispatches per-declaration: type / error bodies
// for field-level rules, services / methods for method-level rules.
func (a *analyzer) checkDeclCombinations(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
		a.checkDuplicateWireNames(dd.Name, dd.Body)
	case *ast.ErrorDecl:
		a.checkFieldCombinations(dd.Name, dd.Body)
		a.checkDuplicateWireNames(dd.Name, dd.Body)
	case *ast.ServiceDecl:
		for _, m := range dd.Methods() {
			a.checkMethodCombinations(dd.Name, m)
		}
	}
}

// checkFieldCombinations applies the per-field combination checks to
// every Field in a type or error body. Mixin members are skipped - they
// have no decorators of their own.
func (a *analyzer) checkFieldCombinations(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkSingleBinding(parent, f)
		a.checkBindingFieldType(parent, f)
		a.checkBoundOverlap(parent, f)
	}
}

// checkDuplicateWireNames rejects two fields in the same body that bind to
// the same wire name on the same source (`a @query("x")  b @query("x")`).
// The OpenAPI would carry a duplicate (name, in) parameter — an invalid
// spec a client generator rejects — and the binder would read the same
// value into both fields. The same name on DIFFERENT sources is fine (a
// `@query("x")` and a `@header("x")` are distinct parameters).
// canonicalWireName folds a wire name to its collision key. HTTP header names
// are case-insensitive (RFC 7230) and net/http canonicalises them, so two
// fields bound to `X-Trace` and `x-trace` reach the same header — fold header
// names to lower case for the key. Path / query / cookie names are
// case-sensitive on the wire and pass through unchanged.
func canonicalWireName(kind, name string) string {
	if kind == "header" {
		return strings.ToLower(name)
	}
	return name
}

// isBodyVerb reports whether verb carries a request body (POST/PUT/PATCH) —
// the condition under which an undecorated field rides @body rather than
// auto-promoting to @query.
func isBodyVerb(verb string) bool {
	switch strings.ToUpper(verb) {
	case "POST", "PUT", "PATCH":
		return true
	}
	return false
}

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
		key := kind + "\x00" + canonicalWireName(kind, name)
		if prev, dup := seen[key]; dup {
			d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateWireName,
				"%s.%s: @%s(%q) reuses a wire name already bound on the same source — the OpenAPI would carry a duplicate parameter and the binder would read both fields from one value. Use distinct names.",
				parent, f.Name, kind, name)
			d.Related = related(prev, "first bound here")
			continue
		}
		seen[key] = f.Pos
	}
}

// checkDuplicateAutoWireNames catches a wire-name collision that involves an
// AUTO-bound field — an undecorated field promoted to @path (its name matches
// a {segment}) or to @query (on a body-less verb). The per-declaration
// [analyzer.checkDuplicateWireNames] sees only EXPLICIT decorators, so an
// auto-bound field colliding with an explicit (or another auto) binding slips
// through into a silent double-read + a duplicate OpenAPI parameter. This runs
// in method context (route segments + verb) where the auto-binding is known,
// and reports only collisions involving an auto-bound field (explicit/explicit
// is already covered) so the two checks never double-report.
func (a *analyzer) checkDuplicateAutoWireNames(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil {
		return
	}
	td, ok := a.pkg.Types[m.Request.Name.String()]
	if !ok {
		return // cross-package request — not modelled here
	}
	pathSegs := pathSegments(m)
	bodyVerb := isBodyVerb(m.Verb)
	reqName := m.Request.Name.String()
	type binding struct {
		pos  lexer.Position
		auto bool
	}
	seen := map[string]binding{}
	for _, f := range a.flattenRequestFields(td.Body, map[string]bool{}) {
		kind, auto := RequestFieldBinding(f, pathSegs, bodyVerb)
		switch kind {
		case "path", "query", "header", "cookie", "form":
		default:
			continue
		}
		name := WireName(f, kind)
		key := kind + "\x00" + canonicalWireName(kind, name)
		if prev, dup := seen[key]; dup {
			if auto || prev.auto {
				d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateWireName,
					"%s.%s on %s %s: this field auto-binds to @%s(%q), already bound by another field — the binder reads both from one value and the OpenAPI carries a duplicate parameter. Give one an explicit, distinct binding.",
					reqName, f.Name, strings.ToUpper(m.Verb), m.Name, kind, name)
				d.Related = related(prev.pos, "first bound here")
			}
			continue
		}
		seen[key] = binding{pos: f.Pos, auto: auto}
	}
}

// WireName returns the on-the-wire name for field f under binding kind
// (path/query/header/cookie/form): the binding decorator's first non-empty
// string argument, or the field's own name when none is given. It is the one
// rule shared by the analyser's binding checks and codegen's binders /
// OpenAPI parameter emit, so the documented parameter name and the name the
// handler actually reads cannot disagree.
func WireName(f *ast.Field, kind string) string {
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

// BindingKind returns the binding kind named by the first binding decorator
// in ds — "path" / "query" / "header" / "cookie" / "body" / "form" — or "" when
// none is present. It is the single "which decorator binds this field"
// classifier the analyser's binding checks and codegen's binders both read, so
// the two layers cannot disagree on where a field rides. (Valid input carries
// at most one binding decorator per field — the single-binding rule rejects
// the rest — so first-match is unambiguous.)
func BindingKind(ds []*ast.Decorator) string {
	for _, d := range ds {
		if d == nil {
			continue
		}
		switch d.Name {
		case "path", "query", "header", "cookie", "body", "form":
			return d.Name
		}
	}
	return ""
}

// RequestFieldBinding resolves where a request field rides once method
// context is applied: its explicit binding decorator if any (or @sensitive),
// otherwise the auto-binding rule — an un-decorated field auto-binds to "path"
// when its name matches a `{param}` segment, to "query" on a body-less verb
// (there is no body to decode into), or stays "body". auto is true only for an
// auto-promoted path/query field. This is the single place the request
// auto-binding rule lives, read by both the analyser's binding checks and
// codegen's request resolver so the two cannot disagree on where a field rides.
func RequestFieldBinding(f *ast.Field, pathNames map[string]bool, bodyVerb bool) (kind string, auto bool) {
	if ast.HasDecorator(f.Decorators, "sensitive") {
		return "sensitive", false
	}
	if k := BindingKind(f.Decorators); k != "" {
		return k, false
	}
	switch {
	case pathNames[f.Name]:
		return "path", true
	case !bodyVerb:
		return "query", true
	default:
		return "body", false
	}
}

// wireBinding returns the wire (kind, name) a field binds to: the binding
// decorator's explicit string arg, or the field name when the arg is
// absent. bound is false for an unbound (body) field.
func wireBinding(f *ast.Field) (kind, name string, bound bool) {
	switch k := BindingKind(f.Decorators); k {
	case "path", "query", "header", "cookie", "form":
		return k, WireName(f, k), true
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

// checkBindingFieldType vets the type compatibility of `@path`,
// `@header`, `@cookie`, and `@form` bindings up front so the codegen
// never has to produce uncompilable Go.
//
// Per-decorator rules (mirrors the wire-bind codegen in
// `internal/codegen.renderWireBindLine`):
//
//   - `@path`              — the same wire-bindable shapes as @query
//     (string / bool / int* / uint* / float*, or a scalar / enum over
//     one), but never optional or array. Path segments are mandatory by
//     definition (the route matched or it didn't), so optional makes no
//     semantic sense, and a path carries one value per segment. A
//     numeric segment is parsed via the same server.Parse* helper a
//     numeric @query field uses.
//   - `@query` / `@header` / `@cookie` — string + numeric + bool +
//     scalars/enums + arrays of those. Optional string-shaped is
//     accepted (binder emits `*T`); optional numerics use the
//     zero-value sentinel because tri-state pointers off a string-
//     wire are not unambiguous.
//   - `@form`              — same as @query plus the `file` type
//     (multipart upload path). Arrays of file are still rejected
//     because the binder writes a single `*multipart.FileHeader`.
//
// Anything outside these categories raises [CodeBindingType] with a
// message that names the offending shape so the author can repair
// without consulting docs.
func (a *analyzer) checkBindingFieldType(parent string, f *ast.Field) {
	if f.Type == nil {
		return
	}
	// `@nullable` marks a JSON-body field as accepting an explicit null.
	// A wire parameter (path / query / header / cookie / form) is a string
	// on the wire with no JSON-null form, and the Go field it lowers to
	// would be a pointer the wire binder can't assign — so the pairing is
	// rejected outright. `?` is the way to make a parameter optional.
	if ast.HasDecorator(f.Decorators, "nullable") {
		for _, d := range f.Decorators {
			switch d.Name {
			case "path", "query", "header", "cookie", "form":
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
					"@nullable cannot be combined with @%s: a wire parameter is a string with no JSON-null form. Use `?` to make the parameter optional.",
					d.Name)
				return
			}
		}
	}
	// A path segment of a matched route is ALWAYS supplied, so @default can
	// never apply. The auto-@path form is rejected by checkAutoPathField;
	// reject the explicit @path form here too so the two forms agree
	// (otherwise codegen emits a dead prefill and the OpenAPI param carries
	// both required:true and a default).
	if ast.HasDecorator(f.Decorators, "default") {
		for _, d := range f.Decorators {
			if d.Name == "path" {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
					"@default cannot be combined with @path: a path segment is always supplied for a matched route, so the default can never apply — drop it.")
				return
			}
		}
	}
	// A wire-string source (@query / @header / @form) encodes an array
	// as repeated scalar params (`?x=1&x=2`) — inherently one-dimensional.
	// A nested array (`int[][]`) has no wire form, so reject it
	// structurally here, before the qualified-ref skip below: array depth
	// is independent of the element type, so the check is the same whether
	// the element is local or cross-package. Without this, codegen emits a
	// 1-D binder against an N-D field that won't compile. (@cookie / @path
	// reject every array shape outright elsewhere.)
	if f.Type.ArrayDepth > 1 {
		for _, d := range f.Decorators {
			switch d.Name {
			case "query", "header", "form":
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
					"field %s.%s: @%s cannot bind to a multi-dimensional array - a wire parameter carries repeated single values (`?x=1&x=2`), which has no nested form. Move the field to the JSON body or flatten to a single-level array.",
					parent, f.Name, d.Name)
				return
			}
		}
	}
	// In project mode the per-package pass defers qualified-ref
	// binding-type checks to the post-pass resolver, which has the
	// full project symbol table. Without the skip a cross-pkg scalar
	// like `id shared.Email @path` false-rejects because the local
	// pkg.Scalars map can't see `shared.Email`. See
	// [refResolver.checkProjectBindings] for the cross-pkg-aware
	// re-check.
	if a.opts.skipBindingTypeCheckQualified && isQualifiedTypeRef(f.Type) {
		return
	}
	for _, d := range f.Decorators {
		switch d.Name {
		case "path":
			if isPathBindingType(f.Type, a.pkg) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				"field %s.%s: @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
			return
		case "query", "header", "cookie":
			// Cookie has no multi-value shape; reject arrays with
			// the source-specific message BEFORE the general wire
			// check (which accepts arrays for query / header).
			if d.Name == "cookie" && f.Type.Array {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
					"field %s.%s: @cookie cannot bind to an array - cookies carry a single value per name",
					parent, f.Name)
				return
			}
			if isWireBindingType(f.Type, a.pkg) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				"field %s.%s: @%s requires string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or generic instantiations) - got %s",
				parent, f.Name, d.Name, describeTypeRef(f.Type))
			return
		case "form":
			if isFormBindingType(f.Type, a.pkg) {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingType,
				"field %s.%s: @form requires `file` or string/bool/int*/uint*/float*, a scalar/enum wrapping one of those, or an array of those (no maps, structs, or file arrays) - got %s",
				parent, f.Name, describeTypeRef(f.Type))
			return
		}
	}
}

// isQualifiedTypeRef reports whether t names a cross-package symbol
// (`pkg.Name` with 2 segments). Array / optional wrappers don't
// affect the named ref inside — strip those down to the head ref.
func isQualifiedTypeRef(t *ast.TypeRef) bool {
	if t == nil || t.Named == nil || t.Named.Name == nil {
		return false
	}
	return len(t.Named.Name.Parts) >= 2
}

// isPathBindingType reports whether t can bind to `@path`. A path
// segment is parsed the same way as a `@query` value (string / bool /
// int* / uint* / float*, or a scalar / enum wrapping one), so the
// accepted set is exactly [isWireBindingType] MINUS two shapes a URL
// path can't carry:
//   - optional: a matched route always supplies the segment, so a
//     nilable path field is meaningless.
//   - array: a path carries a single value per segment, with no
//     repeated form.
//
// Numeric path IDs (`/users/{id}` with `id int`) are the common REST
// case; the binder parses the segment via the same server.Parse* helper
// a numeric @query field uses.
func isPathBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Optional || t.Array {
		return false
	}
	return isWireBindingType(t, pkg)
}

// isWireBindingType reports whether t is acceptable as a `@query`,
// `@header`, or `@cookie` field. The shared set covers every primitive
// the codegen's wire-bind shape catalogue can parse:
//
//   - string                              → directSingle / optionalStringNoCast
//   - bool / int* / uint* / float*        → singleParsed
//   - string-backed scalar / enum         → directSingle / optionalStringCast with cast
//   - numeric scalar / int enum           → singleParsed with cast
//   - array of any of the above           → directSlice / arrayString / arrayParsed
//   - optional of any string-shaped item  → optionalString*
//
// Optional numerics are accepted too (the binder writes a `*T` and leaves
// it nil when the key is absent). Rejects: maps, structs, generic
// instantiations, and the `file` type (which only `@form` accepts).
func isWireBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Map != nil || t.Named == nil || len(t.Named.Args) > 0 {
		return false
	}
	// A wire-string source encodes an array as repeated single values
	// (`?x=1&x=2`); a nested array has no wire form. Reject at the shared
	// predicate so every consumer — the explicit `@query`/`@header` check,
	// the auto-@query promotion on a body-less verb, and the @form set —
	// agrees, instead of leaving the depth guard on one path only.
	if t.ArrayDepth > 1 {
		return false
	}
	name := t.Named.Name.String()
	if name == "file" {
		return false
	}
	if isPrimitiveWireName(name) {
		return true
	}
	if pkg == nil {
		return false
	}
	if sc, ok := pkg.Scalars[name]; ok && sc != nil {
		return isPrimitiveWireName(sc.Primitive)
	}
	if ed, ok := pkg.Enums[name]; ok && ed != nil {
		for _, m := range ed.Members {
			if v, ok := m.(*ast.EnumValue); ok {
				switch v.Kind {
				case ast.EnumBare, ast.EnumString, ast.EnumInt:
					return true
				}
			}
		}
	}
	return false
}

// isPrimitiveWireName lists the Go builtin types the wire-bind codegen
// can parse from a single HTTP string. Delegates to
// [idents.IsWireParseable] so semantic-time and gen-time rejections
// share one source of truth - the codegen's `queryPrims` table mirrors
// the same set (semantic mustn't import codegen, so the canonical
// table lives in the type-neutral idents package).
func isPrimitiveWireName(name string) bool {
	return idents.IsWireParseable(name)
}

// isFormBindingType is the wire-bind set plus the `file` type, which
// only multipart supports. `file?` and bare `file` are equivalent
// (the renderer drops the pointer wrap on already-nilable types);
// `file[]` is rejected because the multipart binder writes a single
// `*multipart.FileHeader` slot, not a slice.
func isFormBindingType(t *ast.TypeRef, pkg *Package) bool {
	if t == nil || t.Named == nil {
		return false
	}
	if t.Named.Name.String() == "file" {
		return !t.Array && t.Map == nil
	}
	return isWireBindingType(t, pkg)
}

// describeTypeRef renders a short human label for a TypeRef so binding
// diagnostics can say `got string?` / `got string[]` / `got int`. Kept
// minimal - the diagnostic only needs to point at the mismatch.
func describeTypeRef(t *ast.TypeRef) string {
	if t == nil {
		return "(none)"
	}
	name := "?"
	if t.Named != nil {
		name = t.Named.Name.String()
	} else if t.Map != nil {
		key, val := "?", "?"
		if t.Map.Key != nil {
			key = describeTypeRef(t.Map.Key)
		}
		if t.Map.Value != nil {
			val = describeTypeRef(t.Map.Value)
		}
		name = "map<" + key + ", " + val + ">"
	}
	// Render one `[]` per array dimension so a multi-dim field reads as
	// `int[][]`. ArrayDepth is authoritative; fall back to a single `[]`
	// for any hand-built TypeRef that set only the Array flag.
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		depth = 1
	}
	for i := 0; i < depth; i++ {
		name += "[]"
	}
	if t.Optional {
		name += "?"
	}
	return name
}

// checkSingleBinding enforces the "at most one binding" rule. The
// six binding decorators (`@path / @query / @header / @cookie / @body /
// @form`) are mutually exclusive; the first wins, every subsequent one
// gets a diagnostic with a back-reference to the first.
func (a *analyzer) checkSingleBinding(parent string, f *ast.Field) {
	bindings := map[string]bool{
		"path": true, "query": true, "header": true,
		"cookie": true, "body": true, "form": true,
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

// checkMethodCombinations enforces method-level rules:
//
//   - `@passthrough` methods must not declare a `request` or `response`
//     block - logic handles the wire format directly, so any framework-
//     managed shape would be silently ignored.
func (a *analyzer) checkMethodCombinations(svcName string, m *ast.Method) {
	a.checkPassthroughBody(svcName, m)
	a.checkBodyBindingVerb(svcName, m)
	a.checkDuplicatePathVars(svcName, m)
	a.checkAutoPathField(svcName, m)
	a.checkDuplicateAutoWireNames(svcName, m)
	a.checkNoContentStatusBody(svcName, m)
	a.checkRequestBodyType(svcName, m)
}

// checkRequestBodyType rejects a request type that is a bare scalar or enum
// (a fieldless named type). The request binder/decoder drives off the
// type's FIELDS, so a fieldless type yields no decode and no parameters —
// the client payload is silently dropped (and a constraint-free scalar
// produces non-compiling Go, since the handler calls a Validate() that
// isn't generated). Wrap the value in a `type { value <T> }`. Mirrors the
// existing bare-array request reject. Only local (unqualified) request
// types are resolved here; a qualified cross-package scalar/enum request is
// rare and left to codegen.
func (a *analyzer) checkRequestBodyType(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil || m.Request.Name == nil || len(m.Request.Name.Parts) != 1 {
		return
	}
	name := m.Request.Name.String()
	kind := bareRequestKind(a.pkg, name)
	if kind == "" {
		return
	}
	a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodeBindingType,
		"request type %q is a %s, which has no fields to bind or decode as a request body — wrap it in a type (`type Req { value %s }`)",
		name, kind, name)
}

// bareRequestKind reports whether `name` resolves to a scalar or enum in pkg
// (a fieldless type that has nothing to bind or decode as a request body), or
// "" otherwise. Shared by the per-package and project request-type checks.
func bareRequestKind(pkg *Package, name string) string {
	if pkg == nil {
		return ""
	}
	if _, ok := pkg.Scalars[name]; ok {
		return "scalar"
	}
	if _, ok := pkg.Enums[name]; ok {
		return "enum"
	}
	return ""
}

// checkProjectRequestBodyType is the cross-package twin of
// checkRequestBodyType: a qualified `request shared.Email` whose target is a
// scalar/enum in the sibling package is rejected (the per-package pass only
// resolves a 1-part local name).
func (r *refResolver) checkProjectRequestBodyType() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				if m == nil || m.Request == nil || m.Request.Name == nil || len(m.Request.Name.Parts) != 2 {
					continue
				}
				parts := m.Request.Name.Parts
				kind := bareRequestKind(r.proj.Packages[parts[0]], parts[1])
				if kind == "" {
					continue
				}
				name := m.Request.Name.String()
				r.diag(m.Request.Pos, lexer.SeverityError, CodeBindingType,
					"request type %q is a %s, which has no fields to bind or decode as a request body — wrap it in a type (`type Req { value %s }`)",
					name, kind, name)
			}
		}
	}
}

// checkNoContentStatusBody rejects a no-content success status (204, 304,
// or any 1xx) on a method that declares a response body. Per RFC 9110
// those statuses carry no body, but both the OpenAPI emitter and the
// transport template select their body-emitting branch on response-body
// presence alone — never the status — so the pairing would advertise a
// `application/json` body under a status that forbids one and write a body
// the client never receives.
func (a *analyzer) checkNoContentStatusBody(svcName string, m *ast.Method) {
	if m == nil || m.Response == nil || m.Response.Type == nil {
		return
	}
	for _, d := range m.Decorators {
		if d == nil || d.Name != "status" || len(d.Args) != 1 {
			continue
		}
		il, ok := d.Args[0].Value.(*ast.IntLit)
		if !ok {
			continue
		}
		code := il.Value
		if code == 204 || code == 205 || code == 304 || (code >= 100 && code < 200) {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
				"@status(%d) is a no-content status and cannot carry a response body, but method %s declares one — drop the response, or use a status that allows a body.",
				code, m.Name)
			return
		}
	}
}

// checkAutoPathField rejects optional (`?`) / `@nullable` / `@default` on a
// request field that auto-binds to a `{param}` segment (its name matches the
// segment and it carries no explicit binding decorator). A matched route
// always supplies the segment, so an optional path field is meaningless;
// `@nullable` lowers the field to a pointer while the path binder writes a
// plain string into it (`req.ID = r.PathValue(...)` into a `*string` —
// non-compiling); and `@default` can never apply to an always-present
// segment. The explicit `@path` form is already rejected for these; this
// mirrors it for the implicit auto-@path path, on every verb.
func (a *analyzer) checkAutoPathField(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil || m.Path == nil {
		return
	}
	td, ok := a.pkg.Types[m.Request.Name.String()]
	if !ok {
		return // cross-package request — handled by checkProjectAutoPathField
	}
	pathSegs := pathSegments(m)
	if len(pathSegs) == 0 {
		return
	}
	reqName := m.Request.Name.String()
	emit := func(pos lexer.Position, code, format string, args ...any) {
		a.diag(pos, pos, lexer.SeverityError, code, format, args...)
	}
	// Resolve bindability against the local table; a qualified cross-package
	// type is deferred to the project twin, which sees the foreign package.
	unbindable := func(f *ast.Field) bool {
		return !isQualifiedTypeRef(f.Type) && !isPathBindingType(f.Type, a.pkg)
	}
	for _, f := range a.flattenRequestFields(td.Body, map[string]bool{}) {
		autoPathFieldRule(reqName, pathSegs, f, unbindable, emit)
	}
}

// pathSegments returns the set of `{param}` segment names in a method's
// route.
func pathSegments(m *ast.Method) map[string]bool {
	out := map[string]bool{}
	if m == nil || m.Path == nil {
		return out
	}
	for _, seg := range m.Path.Segments {
		if seg.Param {
			out[seg.Literal] = true
		}
	}
	return out
}

// autoPathFieldRule checks one request field that auto-binds to a path
// segment (its name matches a `{param}` and it carries no explicit binding
// decorator): optional `?` / `@nullable` / `@default` are rejected (a matched
// route always supplies the segment, with no optional / null / default form,
// and `@nullable` lowers to a pointer the path binder can't write a plain
// string into — non-compiling), and a non-bindable field type is rejected
// when resolvable. localPkg resolves an unqualified field type's
// path-bindability; pass nil (project pass) or a qualified type to DEFER the
// type check to codegen. Shared by the per-package and project passes so the
// explicit/auto and local/cross-package forms all agree.
func autoPathFieldRule(reqName string, pathSegs map[string]bool, f *ast.Field, typeUnbindable func(*ast.Field) bool, emit func(pos lexer.Position, code, format string, args ...any)) {
	if f == nil || f.Type == nil {
		return
	}
	if kind, auto := RequestFieldBinding(f, pathSegs, false); kind != "path" || !auto {
		return
	}
	switch {
	case f.Type.Optional:
		emit(f.Pos, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which a matched route always supplies — drop the optional `?` (a path parameter is never absent).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "nullable"):
		emit(f.Pos, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, but @nullable makes it a pointer while the path binder writes a plain string — drop @nullable (a path parameter has no null form).",
			reqName, f.Name, f.Name)
	case ast.HasDecorator(f.Decorators, "default"):
		emit(f.Pos, CodeDecoratorConflict,
			"field %s.%s auto-binds to the path segment {%s}, which is always supplied, so @default can never apply — drop it.",
			reqName, f.Name, f.Name)
	case typeUnbindable != nil && typeUnbindable(f):
		// A path segment carries a single primitive/scalar/enum value; a
		// struct / map / array / generic field that auto-binds to it has no
		// wire form. The caller decides bindability — the per-package pass
		// against its local table (deferring qualified cross-package refs to
		// the project twin), the project twin against the resolved IR (which
		// sees cross-package scalars / enums a local table can't).
		emit(f.Pos, CodeBindingType,
			"field %s.%s auto-binds to the path segment {%s}, but @path requires a non-optional, non-array string/bool/int*/uint*/float* field (or a scalar/enum wrapping one) - got %s",
			reqName, f.Name, f.Name, describeTypeRef(f.Type))
	}
}

// pathBindableIR reports whether a resolved field can source a path segment —
// a single wire-string value: a wire primitive (string/bool/int*/uint*/
// float*), a scalar wrapping one, or an enum. Structs, maps, arrays, bytes,
// any, and file have no path-string form. The optional / array shapes are
// rejected by the structural arms of [autoPathFieldRule] before this runs.
// This is the cross-package twin of [isPathBindingType]: it resolves through
// the IR so a `lib.Scalar` / `lib.Enum` is judged by what it wraps, not
// false-rejected for being unresolvable in the using package's local table.
func pathBindableIR(rf ResolvedField) bool {
	switch rf.Category {
	case CatPrimitive, CatEnum:
		return true
	case CatScalar:
		return isPrimitiveWireName(rf.ResolvedPrim)
	}
	return false
}

// wireBindableIR reports whether a field can ride a @query string. It is the
// cross-package twin of [isWireBindingType]: like a path value the element
// must be a wire primitive / scalar-over-one / enum, but a query also accepts
// a 1-D array (repeated values, `?x=1&x=2`). Maps, generics, and nested
// arrays have no wire form. The element is resolved through the IR so a
// cross-package `lib.Scalar` / `lib.Enum` is judged by what it wraps.
func wireBindableIR(f *ast.Field, proj *Project) bool {
	t := f.Type
	if t == nil || t.Map != nil || t.Named == nil || len(t.Named.Args) > 0 || t.ArrayDepth > 1 {
		return false
	}
	// An array rides as repeated single values, so judge the element type.
	elem := *f
	et := *t
	et.Array = false
	et.ArrayDepth = 0
	elem.Type = &et
	return pathBindableIR(ResolveField(&elem, nil, proj))
}

// checkProjectAutoPathField is the cross-package twin of checkAutoPathField:
// the per-package pass returns early for a QUALIFIED request type
// (`request shared.R`), so without this an auto-path field carrying
// `@nullable` (non-compiling) / `?` / `@default` on a cross-package request
// silently slips through. Only qualified requests are processed here (local
// ones are already covered, and re-checking would double-report). The
// type-bindability arm is deferred (localPkg=nil) — the structural decorator
// checks (the #16 non-compile) need no type resolution.
func (r *refResolver) checkProjectAutoPathField() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for _, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				if m == nil || m.Request == nil || m.Request.Name == nil || m.Path == nil {
					continue
				}
				parts := m.Request.Name.Parts
				if len(parts) != 2 {
					continue // local request — per-package pass owns it
				}
				home := r.proj.Packages[parts[0]]
				if home == nil {
					continue
				}
				td, ok := home.Types[parts[1]]
				if !ok {
					continue
				}
				pathSegs := pathSegments(m)
				if len(pathSegs) == 0 {
					continue
				}
				fields := map[string]*ast.Field{}
				r.collectGroupFieldsProject(parts[0], td.Body, fields, map[string]bool{})
				reqName := m.Request.Name.String()
				emit := func(pos lexer.Position, code, format string, args ...any) {
					r.diag(pos, lexer.SeverityError, code, format, args...)
				}
				// The IR resolves a cross-package field's type (collectGroupFields
				// Project requalified each promoted field to its home package), so
				// a foreign struct / array / map that auto-binds to a path segment
				// is caught here — the gap the per-package pass defers.
				unbindable := func(f *ast.Field) bool {
					return !pathBindableIR(ResolveField(f, nil, r.proj))
				}
				for _, f := range fields {
					autoPathFieldRule(reqName, pathSegs, f, unbindable, emit)
				}
			}
		}
	}
}

// checkDuplicatePathVars rejects a route template that repeats a path
// variable name (`/items/{id}/x/{id}`). net/http's ServeMux panics at
// registration on a duplicate wildcard, so gen would produce a server
// that crashes on boot — caught here at design time instead.
func (a *analyzer) checkDuplicatePathVars(svcName string, m *ast.Method) {
	if m == nil || m.Path == nil {
		return
	}
	seen := map[string]bool{}
	for _, seg := range m.Path.Segments {
		if !seg.Param {
			continue
		}
		if seen[seg.Literal] {
			a.diag(seg.Pos, seg.Pos, lexer.SeverityError, CodeDuplicatePathVar,
				"%s.%s route repeats the path variable {%s}: net/http's ServeMux panics on a duplicate wildcard at registration. Rename one segment.",
				svcName, m.Name, seg.Literal)
			return
		}
		seen[seg.Literal] = true
	}
}

// checkBodyBindingVerb rejects `@body` / `@form` request fields on a
// non-body verb (GET / HEAD / DELETE / OPTIONS). Those handlers never
// decode a request body, so the binder's switch falls through and the
// field is left zero with no error — silent data loss. The OpenAPI side
// likewise omits the requestBody for non-body verbs, so the contract and
// the runtime agree only by both dropping the field. Reject up front.
//
// Resolves the request type from the local package; a cross-package
// request DTO (rare) is left to the codegen pass. Body verbs route
// `@body` through the JSON decoder and `@form` through the multipart
// handler, so the check only fires for the non-body set.
func (a *analyzer) checkBodyBindingVerb(svcName string, m *ast.Method) {
	if m == nil || m.Request == nil {
		return
	}
	switch strings.ToUpper(m.Verb) {
	case "POST", "PUT", "PATCH":
		return // body-bearing verbs decode @body / @form normally
	}
	td, ok := a.pkg.Types[m.Request.Name.String()]
	if !ok {
		return
	}
	// Flatten so a field a request inherits through a mixin is checked too:
	// without this an auto-@query non-bindable field (or a @body / @form
	// field) promoted via a mixin slips past the semantic gate and fails
	// only at the codegen stage with a position-less error the LSP can't
	// surface. Mirrors the codegen request flatten.
	verb := strings.ToUpper(m.Verb)
	reqName := m.Request.Name.String()
	pathSegs := pathSegments(m)
	emit := func(start, end lexer.Position, code, format string, args ...any) {
		a.diag(start, end, lexer.SeverityError, code, format, args...)
	}
	unbindable := func(f *ast.Field) bool {
		return !isQualifiedTypeRef(f.Type) && !isWireBindingType(f.Type, a.pkg)
	}
	for _, f := range a.flattenRequestFields(td.Body, map[string]bool{}) {
		bodyBindingVerbRules(reqName, verb, svcName, pathSegs, f, unbindable, emit)
	}
}

// bodyBindingVerbRules checks one request field of a NON-body-verb method:
// `@body` / `@form` require a body-bearing verb (the handler decodes no body,
// so the field would be silently dropped); an un-decorated field auto-binds
// to @query, where `@nullable` is meaningless (a query string has no
// JSON-null form, and the pointer it lowers to can't take the binder's plain
// string — non-compiling); and a non-bindable auto-@query type is rejected
// when resolvable. The first two are STRUCTURAL (no type resolution) and fire
// for cross-package fields too; the type check is delegated to typeUnbindable
// (the per-package pass resolves against its local table and defers qualified
// cross-package refs, the project twin resolves through the IR). Shared by the
// per-package and project passes.
func bodyBindingVerbRules(reqName, verb, svcName string, pathSegs map[string]bool, f *ast.Field, typeUnbindable func(*ast.Field) bool, emit func(start, end lexer.Position, code, format string, args ...any)) {
	if f == nil {
		return
	}
	for _, d := range f.Decorators {
		if d == nil || (d.Name != "body" && d.Name != "form") {
			continue
		}
		emit(d.Pos, decoratorEnd(d), CodeBindingVerb,
			"field %s.%s: @%s requires a body-bearing verb (POST/PUT/PATCH) — the %s %s handler decodes no request body, so the field would be silently dropped",
			reqName, f.Name, d.Name, verb, svcName)
		break // one diagnostic per field
	}
	if f.Type == nil {
		return
	}
	if kind, auto := RequestFieldBinding(f, pathSegs, false); kind != "query" || !auto {
		return
	}
	if ast.HasDecorator(f.Decorators, "nullable") {
		emit(f.Pos, f.Pos, CodeDecoratorConflict,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but @nullable has no meaning on a wire parameter — a query string has no JSON-null form. Use `?` to make it optional, or switch to a body verb (POST/PUT/PATCH).",
			reqName, f.Name, verb, svcName)
		return
	}
	if typeUnbindable != nil && typeUnbindable(f) {
		emit(f.Pos, f.Pos, CodeBindingType,
			"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but %s can't ride a query string — switch to a body verb (POST/PUT/PATCH) so it rides @body, give it an explicit binding, or change the type",
			reqName, f.Name, verb, svcName, describeTypeRef(f.Type))
	}
}

// checkProjectBodyBindingVerb is the cross-package twin of
// checkBodyBindingVerb: the per-package pass bails for a QUALIFIED request
// type, so a `@body`/`@form` field or an auto-@query `@nullable` field
// (non-compiling) on a cross-package request on a body-less verb slipped
// through. Only qualified requests are processed (local ones owned by the
// per-package pass); the type-bindability arm is deferred (localPkg=nil).
func (r *refResolver) checkProjectBodyBindingVerb() {
	for _, pkg := range r.proj.Packages {
		if pkg == nil {
			continue
		}
		for svcName, si := range pkg.Services {
			if si == nil {
				continue
			}
			for _, m := range si.Methods {
				if m == nil || m.Request == nil || m.Request.Name == nil {
					continue
				}
				switch strings.ToUpper(m.Verb) {
				case "POST", "PUT", "PATCH":
					continue
				}
				parts := m.Request.Name.Parts
				if len(parts) != 2 {
					continue
				}
				home := r.proj.Packages[parts[0]]
				if home == nil {
					continue
				}
				td, ok := home.Types[parts[1]]
				if !ok {
					continue
				}
				verb := strings.ToUpper(m.Verb)
				reqName := m.Request.Name.String()
				pathSegs := pathSegments(m)
				fields := map[string]*ast.Field{}
				r.collectGroupFieldsProject(parts[0], td.Body, fields, map[string]bool{})
				emit := func(start, end lexer.Position, code, format string, args ...any) {
					r.diag(start, lexer.SeverityError, code, format, args...)
				}
				// The IR resolves a cross-package field's element type, so a
				// foreign struct / map / nested array auto-binding to @query is
				// caught here with a position — the gap the per-package pass
				// defers to a position-less codegen error.
				unbindable := func(f *ast.Field) bool {
					return !wireBindableIR(f, r.proj)
				}
				for _, f := range fields {
					bodyBindingVerbRules(reqName, verb, svcName, pathSegs, f, unbindable, emit)
				}
			}
		}
	}
}

// flattenRequestFields returns body's fields with embedded same-package
// mixins expanded recursively, mirroring the codegen request flatten so
// the method-level binding checks see a field a request inherits through a
// mixin. A qualified (cross-package) mixin is skipped here and left to the
// project resolver, matching the per-package analyzer's scope. `seen`
// breaks mixin cycles. Generic-argument substitution is not modelled — the
// binding checks key on the field's decorators and shape, which a generic
// mixin's promoted field carries regardless of the concrete argument.
func (a *analyzer) flattenRequestFields(body []ast.TypeMember, seen map[string]bool) []*ast.Field {
	var out []*ast.Field
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			out = append(out, v)
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil || len(v.Ref.Name.Parts) != 1 {
				continue
			}
			name := v.Ref.Name.Parts[0]
			if seen[name] {
				continue
			}
			seen[name] = true
			if td, ok := a.pkg.Types[name]; ok {
				out = append(out, a.flattenRequestFields(td.Body, seen)...)
			}
		}
	}
	return out
}

// checkPassthroughBody rejects `request` or `response` blocks on any
// method tagged `@passthrough`. The decorator hands the raw
// http.ResponseWriter and *http.Request to logic; declaring a typed
// shape next to it would mislead readers into expecting framework
// validation that never runs.
func (a *analyzer) checkPassthroughBody(svcName string, m *ast.Method) {
	var passPos lexer.Position
	hasPassthrough := false
	for _, d := range m.Decorators {
		if d == nil {
			continue
		}
		if d.Name == "passthrough" {
			hasPassthrough = true
			passPos = d.Pos
			break
		}
	}
	if !hasPassthrough {
		return
	}
	if m.Request != nil {
		diag := a.diag(m.Request.Pos, m.Request.Pos, lexer.SeverityError, CodePassthroughBody,
			"method %s.%s: @passthrough method must not declare request or response - logic handles wire format directly",
			svcName, m.Name)
		diag.Related = related(passPos, "@passthrough declared here")
	}
	if m.Response != nil {
		pos := m.Response.Pos
		if m.Response.Type != nil {
			pos = m.Response.Type.Pos
		}
		diag := a.diag(pos, pos, lexer.SeverityError, CodePassthroughBody,
			"method %s.%s: @passthrough method must not declare request or response - logic handles wire format directly",
			svcName, m.Name)
		diag.Related = related(passPos, "@passthrough declared here")
	}
}
