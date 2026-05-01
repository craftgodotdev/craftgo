package semantic

import (
	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// sensitiveConflicts lists every decorator whose semantics contradict
// `@sensitive`. Validators are pointless because the field never
// crosses the wire (no input to validate, no output to constrain).
// Wire-binding decorators (`@path`, `@query`, `@header`, `@cookie`,
// `@form`) contradict the "server-internal only" intent. `@nullable`
// and `@default` likewise shape wire behaviour that can't apply.
var sensitiveConflicts = map[string]bool{
	"required":          true,
	"length":            true,
	"minLength":         true,
	"maxLength":         true,
	"pattern":           true,
	"format":            true,
	"min":               true,
	"max":               true,
	"range":             true,
	"positive":          true,
	"negative":          true,
	"multipleOf":        true,
	"minItems":          true,
	"maxItems":          true,
	"uniqueItems":       true,
	"requiresOneOf":     true,
	"mutuallyExclusive": true,
	"nullable":          true,
	"default":           true,
	"path":              true,
	"query":             true,
	"header":            true,
	"cookie":            true,
	"form":              true,
	"body":              true,
}

// checkDecoratorConflicts fires CodeDecoratorConflict for any
// `@sensitive` field that also carries a decorator from the
// [sensitiveConflicts] set. Body iteration covers both `type` and
// `error` declarations - sensitive applies to both surfaces.
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
		hasSensitive := false
		for _, d := range f.Decorators {
			if d != nil && d.Name == "sensitive" {
				hasSensitive = true
				break
			}
		}
		if !hasSensitive {
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
