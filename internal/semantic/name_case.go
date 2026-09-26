package semantic

import (
	"fmt"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// checkDeclNameCase checks the name of every declaration and method of files.
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
		a.checkTypeTableName("type", dd.Name, dd.Pos)
		for _, p := range dd.TypeParams {
			// The generated Go spells the parameter as written.
			a.requireUppercase(p, dd.NamePos, fmt.Sprintf("type parameter %q of %s", p, dd.Name),
				"a lower-case one can hide a package or a variable the generated Go uses, such as fmt or the receiver v")
		}
		a.checkTypeParamsHidePackages(dd)
	case *ast.ErrorDecl:
		a.checkTypeTableName("error", dd.Name, dd.Pos)
	case *ast.EnumDecl:
		a.checkTypeTableName("enum", dd.Name, dd.Pos)
	case *ast.ScalarDecl:
		a.checkTypeTableName("scalar", dd.Name, dd.Pos)
	case *ast.ServiceDecl:
		// An extend block repeats its service's name; its methods are new names.
		if !dd.Extend {
			a.warnServiceNameCase(dd.Name, dd.Pos)
		}
		for _, m := range dd.Methods() {
			a.checkExportedName("method", m.Name, m.Pos)
		}
	case *ast.MiddlewareDecl:
		a.checkExportedName("middleware", dd.Name, dd.Pos)
		if slices.Contains(idents.ServiceContextFields, dd.Name) {
			a.diag(dd.Pos, dd.Pos, lexer.SeverityError, CodeDeclGoNameCollision,
				"middleware %q is named like the field ServiceContext declares itself, which hides the middleware's field - rename it", dd.Name)
		}
	case *ast.EventDecl:
		a.checkExportedName("event", dd.Name, dd.Pos)
	}
}

// checkTypeParamsHidePackages rejects, at td's name, a type parameter of td
// spelled like a package its body names, `w Lib.Item` in `type Box<Lib>`:
// inside the generated type the parameter hides the package. A parameter
// that is no exported name, or a qualifier naming no other package, has its
// own error.
func (a *analyzer) checkTypeParamsHidePackages(td *ast.TypeDecl) {
	hidden := map[string]string{}
	walkTypeRefs(td, func(n *ast.NamedTypeRef, typeParams []string, _ bool) {
		if n.Name == nil || len(n.Name.Parts) != 2 {
			return
		}
		pkg := n.Name.Parts[0]
		imported := pkg != a.pkg.Name && a.proj.Packages[pkg] != nil
		if slices.Contains(typeParams, pkg) && token.IsExported(pkg) && imported && hidden[pkg] == "" {
			hidden[pkg] = n.Name.String()
		}
	})
	for _, p := range td.TypeParams {
		if ref := hidden[p]; ref != "" {
			a.diag(td.NamePos, td.NamePos, lexer.SeverityError, CodeDeclGoNameCollision,
				"type parameter %q of %s hides package %s in the generated type, where %s names it - rename the parameter",
				p, td.Name, p, ref)
		}
	}
}

// checkTypeTableName is [analyzer.checkExportedName] for a type, error, enum
// or scalar, whose built-in spelling [CodeDeclBuiltinName] reports.
func (a *analyzer) checkTypeTableName(kind, name string, pos lexer.Position) {
	if prims.Is(name) {
		return
	}
	a.checkExportedName(kind, name, pos)
}

// checkExportedName rejects a non-empty name that the Go identifiers
// generated from it would carry unexported.
func (a *analyzer) checkExportedName(kind, name string, pos lexer.Position) {
	a.requireUppercase(name, pos, fmt.Sprintf("%s name %q", kind, name),
		"a Go identifier generated from it would be unexported, out of reach of the other generated packages")
}

// requireUppercase rejects a non-empty name that does not start with an
// uppercase letter; what names it in the message and why says what breaks.
func (a *analyzer) requireUppercase(name string, pos lexer.Position, what, why string) {
	if name == "" || token.IsExported(name) {
		return
	}
	fix := "to start with an uppercase letter"
	if exported := exportedSpelling(name); exported != "" {
		fix = strconv.Quote(exported)
	}
	a.diag(pos, pos, lexer.SeverityError, CodeDeclNameCase,
		"%s must start with an uppercase letter: %s - rename it %s", what, why, fix)
}

// warnServiceNameCase warns about a service name that does not start with an
// uppercase letter; it names only directories and documents.
func (a *analyzer) warnServiceNameCase(name string, pos lexer.Position) {
	if name == "" || token.IsExported(name) {
		return
	}
	a.diag(pos, pos, lexer.SeverityWarning, CodeDeclNameCase,
		"service name %q should start with an uppercase letter, as the names of its methods must", name)
}

// exportedSpelling returns name without its leading underscores and with an
// upper-case first letter, or "" when no letter leads what remains.
func exportedSpelling(name string) string {
	rest := strings.TrimLeft(name, "_")
	if rest == "" || !unicode.IsLetter(rune(rest[0])) {
		return ""
	}
	return strings.ToUpper(rest[:1]) + rest[1:]
}
