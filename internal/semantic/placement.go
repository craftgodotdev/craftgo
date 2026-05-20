// Decorator placement checks (decl, field, scope).
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

// checkDeclPlacement dispatches placement checks for one top-level
// declaration plus every nested scope it owns.
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
		// Error bodies are server-emitted, so binding decorators
		// (`@path`, `@query`, ...) and input-validators (`@minLength`,
		// `@pattern`, ...) are rejected via the narrower
		// [LvlErrorField] site.
		a.checkFieldPlacement(LvlErrorField, dd.Name, dd.Body)
	case *ast.ScalarDecl:
		a.checkPlacement(LvlScalar, "scalar "+dd.Name, dd.Decorators)
	case *ast.MiddlewareDecl:
		a.checkPlacement(LvlMiddleware, "middleware "+dd.Name, dd.Decorators)
	case *ast.ServiceDecl:
		// `extend service` cannot carry service-level decorators (rejected
		// by [mergeServices]); we still walk methods so placement on
		// extended methods is checked.
		if !dd.Extend {
			a.checkPlacement(LvlService, "service "+dd.Name, dd.Decorators)
		}
		for _, m := range dd.Methods() {
			a.checkPlacement(LvlMethod, "method "+dd.Name+"."+m.Name, m.Decorators)
		}
	}
}

// checkFieldPlacement applies the placement check to every Field in a
// type or error body. Mixin members carry no decorators and are skipped.
// site is [LvlField] for type bodies and [LvlErrorField] for error
// bodies - the latter rejects request-binding and input-validator
// decorators that don't make sense on server-emitted payloads.
func (a *analyzer) checkFieldPlacement(site Level, parent string, members []ast.TypeMember) {
	for _, m := range members {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		a.checkPlacement(site, site.Name()+" "+parent+"."+f.Name, f.Decorators)
	}
}

// checkPlacement is the leaf: for every decorator in decs, look up the
// registry and emit `decorator/unknown` or `decorator/placement` as
// appropriate. site is the bit for the current declaration site;
// scopeLabel is a human-readable phrase for the diagnostic message
// (e.g. "field User.name").
//
// Nil entries are tolerated for symmetry with [checkDecoratorScope] -
// the parser doesn't produce them today but the defensive guard keeps a
// future regression from crashing the analyser.
func (a *analyzer) checkPlacement(site Level, scopeLabel string, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		spec, known := Lookup(d.Name)
		if !known {
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
