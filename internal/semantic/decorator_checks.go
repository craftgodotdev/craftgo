// Decorator duplicate / scope / conflict / sensitive checks.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkDecoratorDuplicates(files []*ast.File) {
	for _, f := range files {
		a.checkDecoratorScope("file", f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclDecorators(d)
		}
	}
}

// checkDeclDecorators dispatches decorator-uniqueness checks for one
// top-level declaration plus every nested scope it owns (fields, methods,
// enum values).
func (a *analyzer) checkDeclDecorators(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkDecoratorScope("type "+dd.Name, dd.Decorators)
		a.checkFieldDecorators(dd.Name, dd.Body)
	case *ast.EnumDecl:
		a.checkDecoratorScope("enum "+dd.Name, dd.Decorators)
		for _, v := range dd.EnumValues() {
			a.checkDecoratorScope("enum value "+dd.Name+"."+v.Name, v.Decorators)
		}
	case *ast.ErrorDecl:
		a.checkDecoratorScope("error "+dd.Name, dd.Decorators)
		a.checkFieldDecorators(dd.Name, dd.Body)
	case *ast.ScalarDecl:
		a.checkDecoratorScope("scalar "+dd.Name, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkDecoratorScope("middleware "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		scope := "service " + dd.Name
		if dd.Extend {
			scope = "extend " + scope
		}
		a.checkDecoratorScope(scope, dd.Decorators)
		for _, m := range dd.Methods() {
			a.checkDecoratorScope("method "+dd.Name+"."+m.Name, m.Decorators)
		}
	}
}

// checkFieldDecorators applies the duplicate check to every Field in a type
// or error body. Mixin members carry no decorators and are skipped.
func (a *analyzer) checkFieldDecorators(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkDecoratorScope("field "+parent+"."+f.Name, f.Decorators)
	}
}

// checkDecoratorScope is the leaf check: emit a diagnostic for any decorator
// whose Name appears more than once in decs. The first occurrence is silent;
// every subsequent one is flagged with a Related link to the first so the
// IDE can render a clickable cross-reference.
//
// Repeatable decorators (`@security`, `@tags`, `@middlewares`) bypass the
// check: each instance is its own semantic contribution (OR alternative
// for security, additional tag, additional middleware in the chain) and
// the extend-service propagation naturally produces multiple instances
// of the same name when a method-level decorator merges with one inherited
// from an `extend service` block.
func (a *analyzer) checkDecoratorScope(scope string, decs []*ast.Decorator) {
	seen := map[string]lexer.Position{}
	for _, d := range decs {
		if d == nil {
			continue
		}
		if Registry[d.Name].Repeatable {
			continue
		}
		if prev, ok := seen[d.Name]; ok {
			diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorDuplicate,
				"duplicate decorator @%s on %s", d.Name, scope)
			diag.Related = related(prev, "first decorator here")
			continue
		}
		seen[d.Name] = d.Pos
	}
}

// checkDecoratorConflicts fires CodeDecoratorConflict for any field
// that pairs `@sensitive` with a wire-shaping decorator. The conflict
// table lives next to [Registry] in decorators.go; this function only
// walks the AST and emits the diagnostic.
func (a *analyzer) checkDecoratorConflicts(files []*ast.File) {
	for _, f := range files {
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.TypeDecl:
				a.checkSensitiveConflictsIn(dd.Body)
			case *ast.ErrorDecl:
				a.checkSensitiveConflictsIn(dd.Body)
			}
		}
	}
}

// checkSensitiveConflictsIn walks a type / error body once. For every
// field that carries `@sensitive`, every other decorator listed in
// [sensitiveConflicts] becomes a CodeDecoratorConflict diagnostic.
func (a *analyzer) checkSensitiveConflictsIn(members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		if !ast.HasDecorator(f.Decorators, "sensitive") {
			continue
		}
		for _, d := range f.Decorators {
			if d == nil || d.Name == "sensitive" {
				continue
			}
			if !sensitiveConflicts[d.Name] {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorConflict,
				"@%s cannot be combined with @sensitive: sensitive fields never cross the wire",
				d.Name)
		}
	}
}
