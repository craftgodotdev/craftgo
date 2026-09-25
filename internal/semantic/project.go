package semantic

import (
	"cmp"
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Project is the analysis of a whole design. Packages is keyed by
// [Package.Name]; files in any folder that declare one name share an entry.
type Project struct {
	Packages map[string]*Package
	// typeParams holds each reference to a type parameter in a generic
	// type's body; [Project.resolve] finds no declaration for one.
	typeParams map[*ast.QualifiedIdent]bool
}

// AnalyzeProject groups files into packages by their `package`
// declaration, analyses every package with the whole project in scope,
// and runs the project-wide rules. A file without a `package` clause joins
// no package. The Project is never nil.
func AnalyzeProject(files []*ast.File, opts Options) (*Project, []Diagnostic) {
	proj := &Project{Packages: map[string]*Package{}}
	groups, diags := groupFilesByPackage(files)
	names := slices.Sorted(maps.Keys(groups))
	analyzers := make(map[string]*analyzer, len(groups))
	for _, name := range names {
		a := newAnalyzer(proj, name, opts)
		a.runDeclPhase(groups[name])
		proj.Packages[name] = a.pkg
		analyzers[name] = a
	}
	proj.typeParams = typeParamRefs(proj.Packages)
	for _, name := range names {
		a := analyzers[name]
		group := groups[name]
		a.runNamingPhase(group)
		a.runDecoratorPhase(group)
		a.runShapePhase(group)
		a.runRefPhase(group)
		diags = append(diags, a.diags...)
	}
	c := &projectChecks{proj: proj, diags: diags, basePath: opts.BasePath, fileCase: opts.FileCase}
	if opts.DesignRoot != "" {
		c.manifest = lexer.Position{Filename: filepath.Join(opts.DesignRoot, config.Filename)}
	}
	c.checkBasePathFormat()
	c.checkBasePathPattern()
	c.checkProjectGroupChecks()
	c.checkProjectMiddlewareUniqueness()
	c.checkProjectPathCollision()
	c.checkProjectOperationIDUniqueness()
	c.checkProjectEvents()
	c.checkPackageCycles()
	sortDiagnostics(c.diags)
	return proj, c.diags
}

// projectChecks runs the rules that span packages and collects their
// diagnostics.
type projectChecks struct {
	proj     *Project
	diags    []Diagnostic
	basePath string // [Options.BasePath]
	// fileCase is output.fileCase, which names an ungrouped service's directory.
	fileCase string
	// manifest is where a diagnostic about a manifest value points: the
	// manifest file, without a line; the zero position without a design root.
	manifest lexer.Position
}

// diag appends a diagnostic at pos and returns a pointer into c.diags for
// setting Related; the pointer is invalid after the next append.
func (c *projectChecks) diag(pos lexer.Position, sev lexer.Severity, code, format string, args ...any) *Diagnostic {
	c.diags = append(c.diags, Diagnostic{
		Pos:      pos,
		End:      pos,
		Severity: sev,
		Code:     code,
		Msg:      fmt.Sprintf(format, args...),
	})
	return &c.diags[len(c.diags)-1]
}

// siteReport is one site of a problem reported at every site: the message
// there, and the note the other sites relate it by.
type siteReport struct {
	pos  lexer.Position
	msg  string
	note string
	// peer groups sites that relate none of their group; "" relates every site.
	peer string
}

// reportEverySite reports each site's message under code, relating every
// other site by its note.
func (c *projectChecks) reportEverySite(code string, sites []siteReport) {
	for i, s := range sites {
		d := c.diag(s.pos, lexer.SeverityError, code, "%s", s.msg)
		for j, o := range sites {
			if j == i || (s.peer != "" && o.peer == s.peer) {
				continue
			}
			d.Related = append(d.Related, lexer.Related{Pos: o.pos, Msg: o.note})
		}
	}
}

// sortDiagnostics orders diags by position, code and message.
func sortDiagnostics(diags []Diagnostic) {
	slices.SortStableFunc(diags, func(a, b Diagnostic) int {
		return cmp.Or(
			comparePos(a.Pos, b.Pos),
			cmp.Compare(a.Code, b.Code),
			cmp.Compare(a.Msg, b.Msg),
		)
	})
}

// comparePos orders positions by file, then offset.
func comparePos(a, b lexer.Position) int {
	return cmp.Or(cmp.Compare(a.Filename, b.Filename), cmp.Compare(a.Offset, b.Offset))
}

// groupFilesByPackage groups files by their `package` name and reports each
// file that declares something without a `package` clause, or names a
// package Go cannot use. A clause whose name did not parse joins no group
// either; the parser reported it.
func groupFilesByPackage(files []*ast.File) (map[string][]*ast.File, []Diagnostic) {
	groups := map[string][]*ast.File{}
	var diags []Diagnostic
	for _, f := range files {
		switch {
		case f.Package == nil:
			if pos, ok := firstDeclarationPos(f); ok {
				diags = append(diags, Diagnostic{
					Pos:      pos,
					End:      pos,
					Severity: lexer.SeverityError,
					Code:     CodePackageMissing,
					Msg:      "this file has no `package` clause - every design file starts with `package <name>`",
				})
			}
		case f.Package.Name != "":
			if why := goPackageNameProblem(f.Package.Name); why != "" {
				diags = append(diags, Diagnostic{
					Pos:      f.Package.Pos,
					End:      f.Package.Pos,
					Severity: lexer.SeverityError,
					Code:     CodePackageName,
					Msg:      fmt.Sprintf("package name %q %s - rename the package", f.Package.Name, why),
				})
			}
			groups[f.Package.Name] = append(groups[f.Package.Name], f)
		}
	}
	return groups, diags
}

// goPackageNameProblem says why no generated Go package can take name, the
// DSL package's name, or returns "".
func goPackageNameProblem(name string) string {
	switch {
	case token.IsKeyword(name):
		return "is a Go keyword, which a Go package clause cannot hold"
	case name == "_":
		return "is Go's blank identifier, which names no package"
	case name == "main":
		return "makes a Go program, which the other generated packages cannot import"
	case name == "init":
		return "is reserved for Go's init functions, so no Go file can import a package of that name"
	case types.Universe.Lookup(name) != nil:
		return "is predeclared in Go, so a generated file importing the package would lose the built-in " + name
	}
	return ""
}

// firstDeclarationPos returns the position of f's first import or
// declaration; ok is false when f declares nothing.
func firstDeclarationPos(f *ast.File) (lexer.Position, bool) {
	switch {
	case len(f.Imports) > 0:
		return f.Imports[0].Pos, true
	case len(f.Decls) > 0:
		return f.Decls[0].DeclPos(), true
	}
	return lexer.Position{}, false
}

// folderExists reports whether importPath, relative to designRoot, is a
// directory holding at least one design file.
func folderExists(designRoot, importPath string) bool {
	if designRoot == "" || importPath == "" {
		return false
	}
	full := filepath.Join(designRoot, filepath.FromSlash(importPath))
	info, err := os.Stat(full)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && config.IsDesignFile(e.Name()) {
			return true
		}
	}
	return false
}
