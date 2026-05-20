// Decorator-specific arg validators with non-uniform shapes: @security, @examples, @externalDocs.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (a *analyzer) checkSecurityArgs(d *ast.Decorator) {
	pos := positionalArgs(d)
	if len(pos) != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@security expects exactly 1 scheme name (got %d)", len(pos))
		return
	}
	if _, ok := pos[0].Value.(*ast.IdentExpr); !ok {
		a.diag(pos[0].Pos, pos[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@security arg 1: expected scheme identifier, got %s", exprKindName(pos[0].Value))
	}
	for _, ag := range d.Args {
		if !ag.Named {
			continue
		}
		if ag.Name != "scopes" {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@security: unexpected named argument %q (only `scopes` is supported)", ag.Name)
			continue
		}
		arr, ok := ag.Value.(*ast.ArrayLit)
		if !ok {
			a.diag(ag.Pos, ag.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@security scopes: expected array of strings, got %s", exprKindName(ag.Value))
			continue
		}
		for i, el := range arr.Elements {
			if _, ok := el.(*ast.StringLit); !ok {
				a.diag(el.ExprPos(), el.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
					"@security scopes[%d]: expected string, got %s", i, exprKindName(el))
			}
		}
	}
}

// checkExamplesArgs handles `@examples({name1: v1, name2: v2})` -
// exactly one object-literal arg.
func (a *analyzer) checkExamplesArgs(d *ast.Decorator) {
	if len(d.Args) != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@examples expects exactly 1 object argument (got %d)", len(d.Args))
		return
	}
	if d.Args[0].Object == nil {
		a.diag(d.Args[0].Pos, d.Args[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@examples expects a {name: value, ...} object literal")
	}
}

// checkExternalDocsArgs handles three accepted forms:
//
//   - `@externalDocs("url")`                                   (positional)
//   - `@externalDocs(url: "...", description: "...")`          (named)
//   - `@externalDocs({url: "...", description: "..."})`        (object)
//
// Allowed keys for the named/object forms are `url` and `description`,
// both of which must be string literals. The positional form accepts a
// single URL string.
func (a *analyzer) checkExternalDocsArgs(d *ast.Decorator) {
	if len(d.Args) == 0 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@externalDocs expects at least 1 argument")
		return
	}
	// Object-literal form.
	if len(d.Args) == 1 && d.Args[0].Object != nil {
		a.checkExternalDocsKVs(d.Args[0].Object)
		return
	}
	// All-named form: every arg must be `url:` or `description:`.
	allNamed := true
	for _, ag := range d.Args {
		if !ag.Named {
			allNamed = false
			break
		}
	}
	if allNamed {
		fields := make([]*ast.ObjectField, 0, len(d.Args))
		for _, ag := range d.Args {
			fields = append(fields, &ast.ObjectField{Pos: ag.Pos, Name: ag.Name, Value: ag.Value})
		}
		a.checkExternalDocsKVs(fields)
		return
	}
	// Positional form: exactly 1 string.
	if len(d.Args) != 1 {
		a.diag(d.Pos, decoratorEnd(d), lexer.SeverityError, CodeDecoratorArity,
			"@externalDocs positional form expects exactly 1 URL string (got %d args)", len(d.Args))
		return
	}
	if _, ok := d.Args[0].Value.(*ast.StringLit); !ok {
		a.diag(d.Args[0].Pos, d.Args[0].Pos, lexer.SeverityError, CodeDecoratorArgType,
			"@externalDocs: expected URL string, got %s", exprKindName(d.Args[0].Value))
	}
}

// checkExternalDocsKVs validates a {url, description} key/value list,
// shared by the object-literal and all-named forms.
func (a *analyzer) checkExternalDocsKVs(fields []*ast.ObjectField) {
	for _, of := range fields {
		if of.Name != "url" && of.Name != "description" {
			a.diag(of.Pos, of.Pos, lexer.SeverityError, CodeDecoratorArgType,
				"@externalDocs: unknown key %q (allowed: url, description)", of.Name)
			continue
		}
		if _, ok := of.Value.(*ast.StringLit); !ok {
			a.diag(of.Value.ExprPos(), of.Value.ExprPos(), lexer.SeverityError, CodeDecoratorArgType,
				"@externalDocs.%s: expected string, got %s", of.Name, exprKindName(of.Value))
		}
	}
}
