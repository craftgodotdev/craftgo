package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkDecoratorPlacement(files []*ast.File) {
	for _, f := range files {
		a.checkPlacement(LvlFile, "file", f.Decorators)
		for _, d := range f.Decls {
			a.checkDeclPlacement(d)
		}
	}
}

// checkDeclPlacement checks the decorators of d and of every scope nested in it.
func (a *analyzer) checkDeclPlacement(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.checkPlacement(LvlType, "type "+dd.Name, dd.Decorators)
		a.checkFieldPlacement(LvlField, dd.Name, dd.Body)
	case *ast.EnumDecl:
		a.checkPlacement(LvlEnum, "enum "+dd.Name, dd.Decorators)
		for _, v := range dd.EnumValues() {
			a.checkPlacement(LvlEnumValue, "enum value "+dd.Name+"."+v.Name, v.Decorators)
		}
	case *ast.ErrorDecl:
		a.checkPlacement(LvlError, "error "+dd.Name, dd.Decorators)
		a.checkFieldPlacement(LvlErrorField, dd.Name, dd.Body)
	case *ast.ScalarDecl:
		a.checkPlacement(LvlScalar, "scalar "+dd.Name, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkPlacement(LvlMiddleware, "middleware "+dd.Name, dd.Decorators)
	case *ast.EventDecl:
		a.checkPlacement(LvlEvent, "event "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		// mergeServices checks the levels of an extend block's own decorators.
		if !dd.Extend {
			a.checkPlacement(LvlService, "service "+dd.Name, dd.Decorators)
		}
		for _, m := range dd.Methods() {
			a.checkPlacement(LvlMethod, methodLabel(dd.Name, m), m.Decorators)
		}
	}
}

// checkFieldPlacement checks each field's decorators against site:
// [LvlField] in a type body, [LvlErrorField] in an error body.
func (a *analyzer) checkFieldPlacement(site Level, parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkPlacement(site, site.Name()+" "+parent+"."+f.Name, f.Decorators)
	}
}

// checkPlacement reports each decorator in decs that is unknown, removed or
// not allowed at site; scopeLabel names the site ("field User.name").
func (a *analyzer) checkPlacement(site Level, scopeLabel string, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		spec, known := Lookup(d.Name)
		if !known {
			if note, gone := RemovedDecorator(d.Name); gone {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorRemoved,
					"@%s on %s is no longer a craftgo decorator. %s", d.Name, scopeLabel, note)
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorUnknown,
				"unknown decorator @%s on %s (not in the framework registry)", d.Name, scopeLabel)
			continue
		}
		if spec.Levels&site == 0 {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorPlacement,
				"@%s is not allowed on %s; valid sites: %s", d.Name, scopeLabel, spec.Levels)
		}
	}
}
