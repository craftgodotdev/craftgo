package semantic

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkJSONNames validates every `@json` and rejects two body fields of
// one type that would share a JSON key.
func (a *analyzer) checkJSONNames(files []*ast.File) {
	for _, f := range files {
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.TypeDecl:
				a.checkJSONNamesIn("type "+dd.Name, dd.Body)
			case *ast.ErrorDecl:
				a.checkJSONNamesIn("error "+dd.Name, dd.Body)
			}
		}
	}
}

func (a *analyzer) checkJSONNamesIn(parent string, members []ast.TypeMember) {
	seen := map[string]*ast.Field{}
	for _, f := range ast.Fields(members) {
		if f.Name == "" {
			continue
		}
		a.checkJSONDecorator(f)
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

func (a *analyzer) checkJSONDecorator(f *ast.Field) {
	for _, d := range f.Decorators {
		if d == nil || d.Name != wire.DecoratorJSON {
			continue
		}
		if kind := wire.BindingKind(f.Decorators); kind != "" && kind != wire.BindingBody {
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
