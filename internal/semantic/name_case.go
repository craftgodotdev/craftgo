package semantic

import (
	"unicode"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// checkDeclNameCase emits a warning for every top-level declaration
// whose identifier does not start with an uppercase letter. The Go
// codegen pass copies decl names verbatim into the generated source,
// so a lower-case DSL name produces an unexported Go type — silently
// breaking cross-package imports the moment a sibling package tries
// to reference the type via `pkg.X`.
//
// The check is a warning rather than an error so projects with an
// established naming convention (e.g. tests that intentionally use
// lower-case decls to assert the codegen behaviour) keep building.
// Production code should still surface the warning prominently in
// IDE squiggles and CI output.
//
// Empty names are skipped — they are already flagged as parser
// recovery artefacts elsewhere and double-reporting only adds noise.
//
// Per-spec: type / error / enum / service / middleware / scalar
// declarations all participate. Method names inside services are
// checked too because they become exported Go function names on the
// service struct (and are referenced from the umbrella RegisterAll).
func (a *analyzer) checkDeclNameCase(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkOneDeclNameCase(d)
		}
	}
}

// checkOneDeclNameCase dispatches per top-level decl shape so the
// kind label in the diagnostic ("type", "service", ...) matches the
// keyword the user typed — better than a generic "declaration"
// because the fix is the same kind-keyword they originally wrote.
func (a *analyzer) checkOneDeclNameCase(d ast.Decl) {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		a.warnNameCase("type", dd.Name, dd.Pos)
	case *ast.ErrorDecl:
		a.warnNameCase("error", dd.Name, dd.Pos)
	case *ast.EnumDecl:
		a.warnNameCase("enum", dd.Name, dd.Pos)
	case *ast.ServiceDecl:
		// `extend service X { ... }` and the original `service X` both
		// produce a ServiceDecl; only the original carries the canonical
		// declaration so warning on Extend would double-report against
		// the same underlying name. Methods added in extend blocks are
		// new names — always check them.
		if !dd.Extend {
			a.warnNameCase("service", dd.Name, dd.Pos)
		}
		for _, m := range dd.Methods {
			if m == nil {
				continue
			}
			a.warnNameCase("method", m.Name, m.Pos)
		}
	case *ast.MiddlewareDecl:
		a.warnNameCase("middleware", dd.Name, dd.Pos)
	case *ast.ScalarDecl:
		a.warnNameCase("scalar", dd.Name, dd.Pos)
	}
}

// warnNameCase records the diagnostic when the supplied name starts
// with anything other than an uppercase letter. Empty names are
// silently ignored — those are parser recovery artefacts.
func (a *analyzer) warnNameCase(kind, name string, pos lexer.Position) {
	if name == "" {
		return
	}
	first := []rune(name)[0]
	if unicode.IsUpper(first) {
		return
	}
	a.diag(pos, pos, lexer.SeverityWarning, CodeDeclNameCase,
		"%s name %q should start with an uppercase letter — codegen emits decl names verbatim, so lower-case becomes an unexported Go identifier (cross-package imports will fail)",
		kind, name)
}
