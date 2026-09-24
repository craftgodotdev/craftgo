package semantic

import (
	"fmt"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDecoratorRefs resolves the names that service and method decorators
// pass to @errors, @middlewares and @security.
func (a *analyzer) checkDecoratorRefs(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkDeclRefs(d)
		}
	}
}

// checkLocalDecoratorRefs checks the field names each type's
// @requiresOneOf and @mutuallyExclusive list.
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

// checkDeclRefs resolves the decorator references of a service and its
// methods.
func (a *analyzer) checkDeclRefs(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		// Checked by checkLocalDecoratorRefs.
	case *ast.ErrorDecl:
		// An error's decorators name no other declaration.
	case *ast.ServiceDecl:
		if dd.Extend {
			// Resolved once on the block, not on each method it is copied to.
			a.checkMemberLevelRefs(dd.Decorators, LvlMethod)
		} else {
			a.checkServiceLevelRefs(dd.Decorators)
		}
		for _, m := range dd.Methods() {
			a.checkMemberLevelRefs(m.Decorators, LvlMethod)
		}
	}
}

// checkFieldGroupRefs checks each field a type's @requiresOneOf or
// @mutuallyExclusive lists: listed once, a field of the type (mixin fields
// included) and fit for a cross-field group.
func (a *analyzer) checkFieldGroupRefs(typeName string, decs []*ast.Decorator, body []ast.TypeMember) {
	var fieldSet map[string]promotedField
	var incomplete bool
	getFields := func() map[string]promotedField {
		if fieldSet != nil {
			return fieldSet
		}
		fieldSet, incomplete = a.promotedFieldSet(a.pkg.Name, body)
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
		seen := map[string]bool{}
		for _, name := range args {
			if seen[name.value] {
				a.diag(name.pos, name.pos, lexer.SeverityWarning, CodeDuplicateGroupField,
					"@%s on type %s: field %q listed more than once",
					d.Name, typeName, name.value)
				continue
			}
			seen[name.value] = true
			pf, ok := getFields()[name.value]
			if !ok {
				if incomplete {
					continue // an unresolved, already reported mixin may promote it
				}
				a.diag(name.pos, name.pos, lexer.SeverityError, CodeDecoratorRef,
					"@%s on type %s: %q is not a field of this type",
					d.Name, typeName, name.value)
				continue
			}
			reportCrossFieldMemberIssues(d.Name, typeName, name.value, ResolveField(pf.Field, a.packageNamed(pf.Pkg), a.proj), func(code, msg string) {
				a.diag(name.pos, name.pos, lexer.SeverityError, code, "%s", msg)
			})
		}
		if d.Name == "mutuallyExclusive" && len(seen) < 2 {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityWarning, CodeMutExSingleField,
				"@mutuallyExclusive needs at least 2 distinct fields (got %d) - the runtime check can never fire",
				len(seen))
		}
	}
}

// reportCrossFieldMemberIssues reports every reason rf cannot join a
// cross-field group; a member without a clean presence check gets only that one.
func reportCrossFieldMemberIssues(decName, typeName, memberName string, rf ResolvedField, report func(code, msg string)) {
	f := rf.Field
	if presenceUnclean(rf) {
		report(CodeCrossFieldNotOptional, fmt.Sprintf(
			"@%s on type %s: field %q has no clean present/absent state for a cross-field group - a slice / map is checked by emptiness (`len(...) > 0`) and a `bytes` / `any` member is always treated as present, while the group's OpenAPI requires it be present and non-null. Reference a pointer-backed field (string, number, bool, struct, enum, or a scalar) instead.",
			decName, typeName, memberName))
		return
	}
	if !f.Type.Optional && !ast.HasDecorator(f.Decorators, "nullable") {
		report(CodeCrossFieldNotOptional, fmt.Sprintf(
			"@%s on type %s: field %q must be optional (`?`) or `@nullable` - a cross-field group needs an unambiguous present/absent state, but a plain field is checked by zero-value emptiness, which disagrees with the OpenAPI schema",
			decName, typeName, memberName))
	}
	if ast.HasDecorator(f.Decorators, "sensitive") {
		report(CodeCrossFieldNotOptional, fmt.Sprintf(
			"@%s on type %s: field %q is @sensitive (server-only, not on the wire), so it can't participate in a cross-field group. Reference a body field instead.",
			decName, typeName, memberName))
	}
	if kind, _, bound := wireBinding(f); bound {
		report(CodeCrossFieldNotOptional, fmt.Sprintf(
			"@%s on type %s: field %q is bound to @%s and does not ride the JSON body, so it can't participate in a body-level cross-field group. Reference a body field instead.",
			decName, typeName, memberName, kind))
	}
	if ast.HasDecorator(f.Decorators, "default") {
		report(CodeCrossFieldNotOptional, fmt.Sprintf(
			"@%s on type %s: field %q carries @default, so it is always present at runtime and the cross-field check is a no-op the OpenAPI contradicts. Drop @default or the cross-field reference.",
			decName, typeName, memberName))
	}
}

// presenceUnclean reports whether a member's presence cannot be a nil
// check: a slice or map is tested by emptiness and bytes or any counts as
// always present. A file or raw field is nil exactly when absent.
func presenceUnclean(rf ResolvedField) bool {
	return rf.IsNilable && rf.Category != CatFile && rf.Category != CatRawBytes
}

// checkServiceLevelRefs resolves a service's @middlewares and @security names.
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

// checkMemberLevelRefs resolves the @errors, @middlewares and @security
// names in decorators written at level lvl. A decorator not allowed at lvl
// is left to the placement check, and a propagated copy to its extend block.
func (a *analyzer) checkMemberLevelRefs(decs []*ast.Decorator, lvl Level) {
	for _, d := range decs {
		if d == nil || d.Propagated {
			continue
		}
		if spec, ok := Lookup(d.Name); !ok || spec.Levels&lvl == 0 {
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

// checkErrorsRef resolves every @errors name: a qualified `pkg.Name` in
// pkg, a bare name in any package.
func (a *analyzer) checkErrorsRef(d *ast.Decorator) {
	for _, arg := range collectIdentOrStringArgs(d) {
		if a.errorDeclared(arg.value) {
			continue
		}
		a.diag(arg.pos, arg.pos, lexer.SeverityError, CodeDecoratorRef,
			"@errors: %q is not a declared error type in any package", arg.value)
	}
}

// checkMiddlewareRef is [analyzer.checkErrorsRef] for @middlewares.
func (a *analyzer) checkMiddlewareRef(d *ast.Decorator) {
	for _, arg := range collectIdentOrStringArgs(d) {
		if a.middlewareDeclared(arg.value) {
			continue
		}
		a.diag(arg.pos, arg.pos, lexer.SeverityError, CodeDecoratorRef,
			"@middlewares: %q is not a declared middleware in any package", arg.value)
	}
}

// checkSecurityRef checks every @security scheme name against
// [Options.SecuritySchemes]; a nil list skips the check.
func (a *analyzer) checkSecurityRef(d *ast.Decorator) {
	if a.opts.SecuritySchemes == nil {
		return
	}
	check := func(name string, pos lexer.Position) {
		if slices.Contains(a.opts.SecuritySchemes, name) {
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

// argName is a name from a decorator argument and where it appears.
type argName struct {
	value string
	pos   lexer.Position
}

// collectIdentOrStringArgs returns d's identifier and string positional
// arguments, array elements included; other literals are skipped.
func collectIdentOrStringArgs(d *ast.Decorator) []argName {
	var out []argName
	for _, ag := range positionalArgs(d) {
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
