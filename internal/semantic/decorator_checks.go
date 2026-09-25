package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDecoratorDuplicates rejects a non-repeatable decorator written twice
// at s; one an extend block gives a method counts as written first there.
func (a *analyzer) checkDecoratorDuplicates(s decoratorSite) {
	seen := map[string]lexer.Position{}
	for _, d := range s.inherited {
		if _, dup := seen[d.Name]; !dup {
			seen[d.Name] = d.Pos
		}
	}
	for _, d := range s.decs {
		if spec, _ := Lookup(d.Name); spec.Repeatable {
			continue
		}
		if prev, ok := seen[d.Name]; ok {
			diag := a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError,
				CodeDecoratorDuplicate,
				"duplicate decorator @%s on %s", d.Name, s.label)
			diag.Related = related(prev, "first decorator here")
			continue
		}
		seen[d.Name] = d.Pos
	}
}

// checkSensitiveConflicts rejects `@sensitive` beside any field decorator
// that is not [Spec.Metadata].
func (a *analyzer) checkSensitiveConflicts(f *ast.Field) {
	if !ast.HasDecorator(f.Decorators, "sensitive") {
		return
	}
	for _, d := range f.Decorators {
		if d.Name == "sensitive" {
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
