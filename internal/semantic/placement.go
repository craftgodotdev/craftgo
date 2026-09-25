package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkPlacement reports each decorator at s that is unknown, removed or not
// allowed there.
func (a *analyzer) checkPlacement(s decoratorSite) {
	for _, d := range s.decs {
		spec, known := Lookup(d.Name)
		switch {
		case !known:
			if note, gone := RemovedDecorator(d.Name); gone {
				a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorRemoved,
					"@%s on %s is no longer a craftgo decorator. %s", d.Name, s.label, note)
				continue
			}
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorUnknown,
				"unknown decorator @%s on %s (not in the framework registry)", d.Name, s.label)
		case s.allows(d.Name):
		case s.extendBlock():
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeExtendDecoratorNotMethod,
				"decorator @%s on extend service %q is not valid on a method; move it to the primary service", d.Name, s.decl.(*ast.ServiceDecl).Name)
		default:
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorPlacement,
				"@%s is not allowed on %s; valid sites: %s", d.Name, s.label, spec.Levels)
		}
	}
}
