package semantic

// Cross-reference validation for decorator arguments. Runs after the
// arity / type check so we know the argument shape is sound; this pass
// asserts the *names* inside that shape resolve to real entities:
//
//   - `@errors(NotFound, Conflict)` - must exist in pkg.Errors
//   - `@middlewares(Auth, RateLimit)` - must exist in pkg.Middlewares
//   - `@requiresOneOf(email, phone)` and `@mutuallyExclusive(...)` -
//     each ident must be a field name in the enclosing type body
//   - `@security(A, B, ...)` - each scheme ident must appear in
//     [Options.SecuritySchemes] (when non-nil). Use `@ignoreSecurity`
//     to opt out of inherited security rather than a sentinel name.
//
// Every miss surfaces as [CodeDecoratorRef] so the IDE can render the
// "unresolved name" squiggle and offer a quick-fix list of candidates.

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDecoratorRefs walks every decorator scope and validates the
// identifier / string arguments that name an entity elsewhere in the
// project. Unknown decorators are skipped - placement / args passes
// already flagged them.
func (a *analyzer) checkDecoratorRefs(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclRefs(d)
		}
	}
}

// checkLocalDecoratorRefs runs only the field-group refs
// (`@requiresOneOf` / `@mutuallyExclusive`) — these never cross
// package boundaries (their targets are same-type field names) so
// they're safe to validate in the per-package pass that
// [AnalyzeProject] runs with `skipMiddlewareRefCheck=true`. A typoed
// field name otherwise reaches codegen, which substitutes a literal
// `false` for the unknown name and produces a validator that never
// fires.
func (a *analyzer) checkLocalDecoratorRefs(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			td, ok := d.(*ast.TypeDecl)
			if !ok {
				continue
			}
			a.checkFieldGroupRefs(td.Name, td.Decorators, td.Body)
		}
	}
}

// checkDeclRefs dispatches by declaration kind. Field-group refs
// (`@requiresOneOf` / `@mutuallyExclusive`) run via
// [checkLocalDecoratorRefs] before this path because they're always
// local — TypeDecl bodies skipped here to avoid double-emission.
// Method / service refs delegate to a shared helper.
func (a *analyzer) checkDeclRefs(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		_ = dd
	case *ast.ErrorDecl:
		// Errors don't currently carry @requiresOneOf or
		// @mutuallyExclusive (the placement matrix gates this); the
		// case stays here so future additions slot in symmetrically.
	case *ast.ServiceDecl:
		if !dd.Extend {
			a.checkServiceLevelRefs(dd.Decorators)
		}
		for _, m := range dd.Methods() {
			a.checkMethodLevelRefs(m)
		}
	}
}

// checkFieldGroupRefs validates `@requiresOneOf` and `@mutuallyExclusive`
// argument idents against the type's actual field names. The body slice
// is walked once to build a name set so multiple decorators on the same
// type don't pay the O(n) cost twice.
func (a *analyzer) checkFieldGroupRefs(typeName string, decs []*ast.Decorator, body []ast.TypeMember) {
	var fieldSet map[string]*ast.Field
	getFields := func() map[string]*ast.Field {
		if fieldSet != nil {
			return fieldSet
		}
		fieldSet = map[string]*ast.Field{}
		// Mixin-promoted fields ARE fields of this type — the host struct
		// embeds them and the validator runs their checks — so a cross-field
		// decorator may reference them, not only the directly-declared ones.
		a.collectGroupFields(body, fieldSet, map[string]bool{})
		return fieldSet
	}
	for _, d := range decs {
		if d == nil {
			continue
		}
		if d.Name != "requiresOneOf" && d.Name != "mutuallyExclusive" {
			continue
		}
		args := collectIdentOrStringArgs(d)
		// Dedupe within the same decorator. Without this,
		// `@requiresOneOf(a, a, b)` emits `v.A == nil && v.A == nil`
		// which `go vet` rejects as a redundant boolean (QF1001),
		// breaking `go test` for any project running vet (the
		// default).
		seen := map[string]bool{}
		for _, name := range args {
			if seen[name.value] {
				a.diag(name.pos, name.pos, lexer.SeverityWarning, CodeDuplicateGroupField,
					"@%s on type %s: field %q listed more than once",
					d.Name, typeName, name.value)
				continue
			}
			seen[name.value] = true
			f, ok := getFields()[name.value]
			if !ok {
				a.diag(name.pos, name.pos, lexer.SeverityError, CodeDecoratorRef,
					"@%s on type %s: %q is not a field of this type",
					d.Name, typeName, name.value)
				continue
			}
			if f != nil {
				// A nilable-but-not-pointer member has no clean cross-field
				// presence: `?` / `@nullable` add no pointer (the Go type is
				// already nilable), so the runtime can't use the `!= nil` check
				// that lines up with the group's OpenAPI present-and-non-null. A
				// slice / map is checked by emptiness (`len(...) > 0`, so an empty
				// `[]` / `{}` reads as absent) and a `bytes` / `any` member has no
				// presence expression at all (always treated as present). Reject
				// so the author references a pointer-backed field instead.
				if presenceUnclean(f) {
					a.diag(name.pos, name.pos, lexer.SeverityError, CodeCrossFieldNotOptional,
						"@%s on type %s: field %q has no clean present/absent state for a cross-field group — a slice / map is checked by emptiness (`len(...) > 0`) and a `bytes` / `any` member is always treated as present, while the group's OpenAPI requires it be present and non-null. Reference a pointer-backed field (string, number, bool, struct, enum, or a scalar) instead.",
						d.Name, typeName, name.value)
					continue
				}
				// The referenced field must be pointer-backed (optional `?` or
				// `@nullable`) so its runtime presence check (`!= nil`) lines up
				// with the present-and-non-null semantics OpenAPI emits for the
				// group. A plain field falls back to zero-value emptiness, which
				// disagrees with the spec.
				if !f.Type.Optional && !ast.HasDecorator(f.Decorators, "nullable") {
					a.diag(name.pos, name.pos, lexer.SeverityError, CodeCrossFieldNotOptional,
						"@%s on type %s: field %q must be optional (`?`) or `@nullable` — a cross-field group needs an unambiguous present/absent state, but a plain field is checked by zero-value emptiness, which disagrees with the OpenAPI schema",
						d.Name, typeName, name.value)
				}
				// A `@sensitive` member is server-only (`json:"-"`, excluded
				// from the schema), so a body-level cross-field group can't
				// reference it: the OpenAPI would name a property the public
				// schema never carries, and the runtime check reads a field the
				// client never sends.
				if ast.HasDecorator(f.Decorators, "sensitive") {
					a.diag(name.pos, name.pos, lexer.SeverityError, CodeCrossFieldNotOptional,
						"@%s on type %s: field %q is @sensitive (server-only, not on the wire), so it can't participate in a cross-field group. Reference a body field instead.",
						d.Name, typeName, name.value)
				}
				// A wire-bound member (`@query`/`@header`/`@cookie`/`@path`/
				// `@form`) is excluded from the JSON body schema, so a body-level
				// cross-field group referencing it advertises a constraint over a
				// property the body never carries — an unsatisfiable / meaningless
				// schema.
				if kind, _, bound := wireBinding(f); bound {
					a.diag(name.pos, name.pos, lexer.SeverityError, CodeCrossFieldNotOptional,
						"@%s on type %s: field %q is bound to @%s and does not ride the JSON body, so it can't participate in a body-level cross-field group. Reference a body field instead.",
						d.Name, typeName, name.value, kind)
				}
				// A `@default` member is pre-filled before decode, so the runtime
				// group is always satisfied while the OpenAPI still requires the
				// client to send it — they disagree on an empty body.
				if ast.HasDecorator(f.Decorators, "default") {
					a.diag(name.pos, name.pos, lexer.SeverityError, CodeCrossFieldNotOptional,
						"@%s on type %s: field %q carries @default, so it is always present at runtime and the cross-field check is a no-op the OpenAPI contradicts. Drop @default or the cross-field reference.",
						d.Name, typeName, name.value)
				}
			}
		}
		// `@mutuallyExclusive` with 0 or 1 distinct fields renders
		// the counter check (`n > 1`) unreachable — dead code that
		// silently never fires. Flag it so the author either adds
		// fields or removes the decorator.
		if d.Name == "mutuallyExclusive" && len(seen) < 2 {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeMutExSingleField,
				"@mutuallyExclusive needs at least 2 distinct fields (got %d) — the runtime check can never fire",
				len(seen))
		}
	}
}

// presenceUnclean reports whether a cross-field member's Go type is nilable
// but not a pointer, so its runtime presence can't be the clean `!= nil`
// check that matches the group's OpenAPI present-and-non-null. A slice / map
// is checked by emptiness (`len(...) > 0`); a raw `bytes` (`[]byte`) or `any`
// (`interface{}`) member has no presence expression and is always treated as
// present. (A `file` is `*multipart.FileHeader` — already a pointer — and a
// scalar over `bytes` lowers to `*Scalar`, so both stay pointer-backed and
// are not flagged here.)
func presenceUnclean(f *ast.Field) bool {
	if f.Type == nil {
		return false
	}
	if f.Type.Array || f.Type.Map != nil {
		return true
	}
	if f.Type.Named != nil {
		switch f.Type.Named.Name.String() {
		case "bytes", "any":
			return true
		}
	}
	return false
}

// collectGroupFields fills out with every field a type body contributes
// (name -> declaration), expanding embedded mixins (recursively) so a
// cross-field decorator can reference a promoted field. `seen` breaks
// mixin cycles; an unresolved mixin ref is skipped (its own decl reports
// the resolution error). A name already present (the host's own field)
// is not overwritten by a promoted one — the host wins, matching Go
// embedding.
func (a *analyzer) collectGroupFields(body []ast.TypeMember, out map[string]*ast.Field, seen map[string]bool) {
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			if _, dup := out[v.Name]; !dup {
				out[v.Name] = v
			}
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			name := v.Ref.Name.String()
			if seen[name] {
				continue
			}
			seen[name] = true
			if td, ok := a.pkg.Types[name]; ok {
				a.collectGroupFields(td.Body, out, seen)
			}
		}
	}
}

// checkServiceLevelRefs validates `@middlewares` and `@security` at the
// service decoration site. The same logic applies on methods via
// [checkMethodLevelRefs].
func (a *analyzer) checkServiceLevelRefs(decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		switch d.Name {
		case "middlewares":
			a.checkMiddlewareRef(d)
		case "security":
			a.checkSecurityRef(d)
		}
	}
}

// checkMethodLevelRefs validates `@errors`, `@middlewares`, and
// `@security` on a single method. Method-level decorators take
// precedence over service-level (per README) but resolution targets are
// the same.
func (a *analyzer) checkMethodLevelRefs(m *ast.Method) {
	for _, d := range m.Decorators {
		if d == nil {
			continue
		}
		switch d.Name {
		case "errors":
			a.checkErrorsRef(d)
		case "middlewares":
			a.checkMiddlewareRef(d)
		case "security":
			a.checkSecurityRef(d)
		}
	}
}

// checkErrorsRef resolves every name passed to `@errors(...)` against
// pkg.Errors. Both bare-ident (`UserNotFound`) and array-shortcut
// (`["UserNotFound", ...]`) forms are accepted by the args pass; we
// flatten via [collectIdentOrStringArgs].
func (a *analyzer) checkErrorsRef(d *ast.Decorator) {
	for _, arg := range collectIdentOrStringArgs(d) {
		if _, ok := a.pkg.Errors[arg.value]; ok {
			continue
		}
		a.diag(arg.pos, arg.pos, lexer.SeverityError, CodeDecoratorRef,
			"@errors: %q is not a declared error type", arg.value)
	}
}

// checkMiddlewareRef resolves middleware names against pkg.Middlewares.
// Same flattening rules as [checkErrorsRef].
func (a *analyzer) checkMiddlewareRef(d *ast.Decorator) {
	for _, arg := range collectIdentOrStringArgs(d) {
		if _, ok := a.pkg.Middlewares[arg.value]; ok {
			continue
		}
		a.diag(arg.pos, arg.pos, lexer.SeverityError, CodeDecoratorRef,
			"@middlewares: %q is not a declared middleware", arg.value)
	}
}

// checkSecurityRef validates every scheme name passed to
// `@security(A, B, ...)` against [Options.SecuritySchemes]. When the
// options list is nil the check is skipped - the LSP runs without a
// loaded manifest in some contexts and we don't want spurious errors.
// To express "this endpoint is public" use `@ignoreSecurity` instead of
// a sentinel scheme name.
func (a *analyzer) checkSecurityRef(d *ast.Decorator) {
	if a.opts.SecuritySchemes == nil {
		return
	}
	check := func(name string, pos lexer.Position) {
		if inSet(name, a.opts.SecuritySchemes) {
			return
		}
		a.diag(pos, pos, lexer.SeverityError, CodeDecoratorRef,
			"@security: scheme %q is not declared in openapi.securitySchemes (known: %s)",
			name, joinQuoted(a.opts.SecuritySchemes))
	}
	for _, ag := range positionalArgs(d) {
		if arr, ok := ag.Value.(*ast.ArrayLit); ok {
			for _, el := range arr.Elements {
				if name, ok := identOrStringValue(el); ok {
					check(name, el.ExprPos())
				}
			}
			continue
		}
		if name, ok := identOrStringValue(ag.Value); ok {
			check(name, ag.Pos)
		}
	}
}

// argName ties a name extracted from a decorator argument to the
// position where it appeared in source. The position is what the IDE
// underlines when the name fails to resolve.
type argName struct {
	value string
	pos   lexer.Position
}

// collectIdentOrStringArgs flattens a decorator's positional arguments
// into a list of (name, position) pairs. It handles three shapes:
//
//   - bare ident / string: `@errors(A, B)` → [A, B]
//   - array literal:       `@errors([A, B])` → [A, B]
//   - mixed:               array element idents are mixed with bare
//
// Non-textual positions (int, etc.) are silently skipped - the args
// pass already flagged them with [CodeDecoratorArgType].
func collectIdentOrStringArgs(d *ast.Decorator) []argName {
	var out []argName
	for _, ag := range positionalArgs(d) {
		// Array shortcut: walk the elements.
		if arr, ok := ag.Value.(*ast.ArrayLit); ok {
			for _, el := range arr.Elements {
				if v, ok := identOrStringValue(el); ok {
					out = append(out, argName{value: v, pos: el.ExprPos()})
				}
			}
			continue
		}
		if v, ok := identOrStringValue(ag.Value); ok {
			out = append(out, argName{value: v, pos: ag.Pos})
		}
	}
	return out
}
