package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkJSONKeys rejects two body fields of one type or error that would
// share a JSON key.
func (a *analyzer) checkJSONKeys(files []*ast.File) {
	for _, f := range files {
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.TypeDecl:
				a.checkJSONKeysIn("type "+dd.Name, dd.Body)
			case *ast.ErrorDecl:
				a.checkJSONKeysIn("error "+dd.Name, dd.Body)
			}
		}
	}
}

func (a *analyzer) checkJSONKeysIn(parent string, members []ast.TypeMember) {
	seen := map[string]*ast.Field{}
	for _, f := range ast.Fields(members) {
		if f.Name == "" {
			continue
		}
		name, presence := wire.JSONShape(f)
		if presence == wire.JSONAbsent {
			continue
		}
		if prev, dup := seen[name]; dup {
			d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFieldNameCollision,
				"field %q of %s carries JSON key %q, which field %q already carries - two members cannot share one key", f.Name, parent, name, prev.Name)
			d.Related = related(prev.Pos, "first carried here")
			continue
		}
		seen[name] = f
	}
}

// checkJSONDecorator rejects `@json` off the body, and a `@json` key that is
// empty or holds whitespace, a quote, a comma or a backslash.
func (a *analyzer) checkJSONDecorator(f *ast.Field) {
	for _, d := range f.Decorators {
		if d.Name != wire.DecoratorJSON {
			continue
		}
		if kind, ok := wire.BindingKind(f.Decorators); ok && kind != wire.BindBody {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorConflict,
				"@json cannot be combined with @%s: a field bound off the body has no JSON key - name the wire location in @%s(...) instead", kind, kind)
		}
		if len(d.Args) == 0 {
			continue
		}
		s, ok := d.Args[0].Value.(*ast.StringLit)
		if !ok {
			continue
		}
		if s.Value == "" || strings.ContainsAny(s.Value, " \t\n\",\\") {
			a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArgValue,
				"@json on field %q must be a non-empty key without whitespace, quotes, commas or backslashes", f.Name)
		}
	}
}
