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
func (a *analyzer) checkDuplicateWireNames(parent string, members []ast.TypeMember) {
	seen := map[string]lexer.Position{}
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		kind, name, bound := wireBinding(f)
		if !bound {
			continue
		}
		key := kind + "\x00" + name
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
	}
	if t.Array {
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
	flat := a.flattenRequestFields(td.Body, map[string]bool{})
	for _, f := range flat {
		for _, d := range f.Decorators {
			if d == nil || (d.Name != "body" && d.Name != "form") {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeBindingVerb,
				"field %s.%s: @%s requires a body-bearing verb (POST/PUT/PATCH) — the %s %s handler decodes no request body, so the field would be silently dropped",
				m.Request.Name.String(), f.Name, d.Name, strings.ToUpper(m.Verb), svcName)
			break // one diagnostic per field
		}
	}
	// An un-decorated field that doesn't match a path segment auto-binds to
	// @query on a non-body verb (there is no body to decode into). Codegen
	// rejects it when its type can't ride a query string — but only at the
	// transport stage, with a position-less error the LSP-shared semantic
	// gate never produces, so the editor shows the design as clean while
	// `craftgo gen` fails. Mirror that rejection here so the two agree.
	pathSegs := map[string]bool{}
	if m.Path != nil {
		for _, seg := range m.Path.Segments {
			if seg.Param {
				pathSegs[seg.Literal] = true
			}
		}
	}
	for _, f := range flat {
		if f.Type == nil {
			continue
		}
		// A qualified cross-package type is deferred to the project pass (the
		// local table can't see a string scalar declared in a sibling package).
		if isQualifiedTypeRef(f.Type) {
			continue
		}
		// Only a field that auto-binds to @query is at risk here: explicitly-
		// bound fields, an auto-@path match (codegen skips a non-bindable one
		// silently), and @sensitive are all resolved elsewhere. RequestFieldBinding
		// is the same rule codegen's request resolver applies.
		if kind, auto := RequestFieldBinding(f, pathSegs, false); kind != "query" || !auto {
			continue
		}
		// `@nullable` lowers the field to a pointer, but on a body-less verb
		// this field auto-binds to @query, where the binder writes a string
		// into a non-pointer slot — the same mismatch the explicit
		// `@nullable @query` pairing is rejected for above. The explicit
		// guard never fires here (there is no binding decorator), so mirror
		// it for the implicit auto-@query path.
		if ast.HasDecorator(f.Decorators, "nullable") {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDecoratorConflict,
				"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but @nullable has no meaning on a wire parameter — a query string has no JSON-null form. Use `?` to make it optional, or switch to a body verb (POST/PUT/PATCH).",
				m.Request.Name.String(), f.Name, strings.ToUpper(m.Verb), svcName)
			continue
		}
		if !isWireBindingType(f.Type, a.pkg) {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeBindingType,
				"field %s.%s: on the %s %s handler this auto-binds to @query (there is no request body to decode into), but %s can't ride a query string — switch to a body verb (POST/PUT/PATCH) so it rides @body, give it an explicit binding, or change the type",
				m.Request.Name.String(), f.Name, strings.ToUpper(m.Verb), svcName, describeTypeRef(f.Type))
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
