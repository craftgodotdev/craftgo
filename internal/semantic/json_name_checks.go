package semantic

import (
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// checkJSONKeys rejects two body fields of one type or error, mixin fields
// included, that would share a JSON key.
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

// checkJSONKeysIn reports each field of members that shares a JSON key with
// an earlier one: at the body's own field when one of the pair is, as
// encoding/json keeps only one of them.
func (a *analyzer) checkJSONKeysIn(parent string, members []ast.TypeMember) {
	own := ast.Fields(members)
	fields, _ := a.proj.flattenFields(a.pkg.Name, a.pkg.Name, members, nil, nil)
	seen := map[string]*ast.Field{}
	for _, ff := range fields {
		f := ff.Field
		if f.Name == "" {
			continue
		}
		name, presence := wire.JSONShape(f)
		if presence == wire.JSONAbsent {
			continue
		}
		prev, dup := seen[name]
		if !dup {
			seen[name] = f
			continue
		}
		at, other := f, prev
		if !slices.Contains(own, f) && slices.Contains(own, prev) {
			at, other = prev, f
		}
		d := a.diag(at.Pos, at.Pos, lexer.SeverityError, CodeFieldNameCollision,
			"field %q of %s carries JSON key %q, which field %q also carries - two members cannot share one key", at.Name, parent, name, other.Name)
		d.Related = related(other.Pos, "also carried here")
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
