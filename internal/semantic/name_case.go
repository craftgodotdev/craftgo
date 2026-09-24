package semantic

import (
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkDeclNameCase warns about each declaration and method name that does
// not start with an uppercase letter, since Go output keeps names verbatim.
func (a *analyzer) checkDeclNameCase(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkOneDeclNameCase(d)
		}
	}
}

// checkOneDeclNameCase checks d's names, labelled with d's keyword.
func (a *analyzer) checkOneDeclNameCase(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.warnNameCase("type", dd.Name, dd.Pos)
	case *ast.ErrorDecl:
		a.warnNameCase("error", dd.Name, dd.Pos)
	case *ast.EnumDecl:
		a.warnNameCase("enum", dd.Name, dd.Pos)
	case *ast.ServiceDecl:
		// An extend block repeats its service's name; its methods are new names.
		if !dd.Extend {
			a.warnNameCase("service", dd.Name, dd.Pos)
		}
		for _, m := range dd.Methods() {
			a.warnNameCase("method", m.Name, m.Pos)
		}
	case *ast.MiddlewareDecl:
		a.warnNameCase("middleware", dd.Name, dd.Pos)
	case *ast.EventDecl:
		a.warnNameCase("event", dd.Name, dd.Pos)
	case *ast.ScalarDecl:
		a.warnNameCase("scalar", dd.Name, dd.Pos)
	}
}

// warnNameCase warns when a non-empty name does not start with an
// uppercase letter.
func (a *analyzer) warnNameCase(kind, name string, pos lexer.Position) {
	if name == "" {
		return
	}
	first := []rune(name)[0]
	if unicode.IsUpper(first) {
		return
	}
	a.diag(pos, pos, lexer.SeverityWarning, CodeDeclNameCase,
		"%s name %q should start with an uppercase letter - codegen emits decl names verbatim, so lower-case becomes an unexported Go identifier (cross-package imports will fail)",
		kind, name)
}
