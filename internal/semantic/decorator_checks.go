package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDecoratorDuplicates rejects a non-repeatable decorator written twice
// on one site, whatever its arguments.
func (a *analyzer) checkDecoratorDuplicates(files []*ast.File) {
	for _, f := range files {
		a.checkDecoratorScope("file", f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclDecorators(d)
		}
	}
}

// checkDeclDecorators checks d and every site nested in it.
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
	case *ast.EventDecl:
		a.checkDecoratorScope("event "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		scope := "service " + dd.Name
		if dd.Extend {
			scope = "extend " + scope
		}
		a.checkDecoratorScope(scope, dd.Decorators)
		for _, m := range dd.Methods() {
			a.checkDecoratorScope(methodLabel(dd.Name, m), m.Decorators)
		}
	}
}

// checkFieldDecorators checks every field of a type or error body.
func (a *analyzer) checkFieldDecorators(parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkDecoratorScope("field "+parent+"."+f.Name, f.Decorators)
	}
}

// checkDecoratorScope reports every repeat of a non-repeatable decorator in
// decs, related to its first occurrence.
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

// checkDecoratorConflicts rejects `@sensitive` beside any field decorator
// that is not [Spec.Metadata].
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
			spec, ok := Lookup(d.Name)
			if !ok || spec.Metadata || spec.Levels&(LvlField|LvlErrorField) == 0 {
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorConflict,
				"@%s cannot be combined with @sensitive: sensitive fields never cross the wire",
				d.Name)
		}
	}
}
