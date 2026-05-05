package semantic

// Cross-reference validation for decorator arguments. Runs after the
// arity / type check so we know the argument shape is sound; this pass
// asserts the *names* inside that shape resolve to real entities:
//
//   - `@errors(NotFound, Conflict)` - must exist in pkg.Errors
//   - `@middlewares(Auth, RateLimit)` - must exist in pkg.Middlewares
//   - `@requiresOneOf(email, phone)` and `@mutuallyExclusive(...)` -
//     each ident must be a field name in the enclosing type body
//   - `@security(scheme[, scopes: [...]])` - scheme must be `noauth` or
//     in [Options.SecuritySchemes] (when non-nil)
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

// checkDeclRefs dispatches by declaration kind. Type-level
// `@requiresOneOf` / `@mutuallyExclusive` need the type body for field
// resolution, so we do that walk inline. Method / service refs delegate
// to a shared helper.
func (a *analyzer) checkDeclRefs(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkFieldGroupRefs(dd.Name, dd.Decorators, dd.Body)
	case *ast.ErrorDecl:
		// Errors don't carry @requiresOneOf/@mutuallyExclusive in v1
		// (placement matrix gates this), but we keep the structure
		// symmetric so future additions slot in.
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
	var fieldSet map[string]bool
	getFields := func() map[string]bool {
		if fieldSet != nil {
			return fieldSet
		}
		fieldSet = map[string]bool{}
		for _, m := range body {
			if f, ok := m.(*ast.Field); ok {
				fieldSet[f.Name] = true
			}
		}
		return fieldSet
	}
	for _, d := range decs {
		if d == nil {
			continue
		}
		if d.Name != "requiresOneOf" && d.Name != "mutuallyExclusive" {
			continue
		}
		for _, name := range collectIdentOrStringArgs(d) {
			if !getFields()[name.value] {
				a.diag(name.pos, name.pos, lexer.SeverityError, CodeDecoratorRef,
					"@%s on type %s: %q is not a field of this type",
					d.Name, typeName, name.value)
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

// checkSecurityRef validates the scheme name passed to `@security(...)`
// against [Options.SecuritySchemes]. The literal `noauth` is always
// accepted (it explicitly marks an endpoint as public). When the
// options list is nil the check is skipped - the LSP runs without a
// loaded manifest in some contexts and we don't want spurious errors.
func (a *analyzer) checkSecurityRef(d *ast.Decorator) {
	if a.opts.SecuritySchemes == nil {
		return
	}
	pos := positionalArgs(d)
	if len(pos) == 0 {
		return
	}
	name, ok := identOrStringValue(pos[0].Value)
	if !ok {
		return
	}
	if name == "noauth" || inSet(name, a.opts.SecuritySchemes) {
		return
	}
	a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorRef,
		"@security: scheme %q is not declared in openapi.securitySchemes (known: %s)",
		name, joinQuoted(a.opts.SecuritySchemes))
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
