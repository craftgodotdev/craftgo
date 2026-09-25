package semantic

import (
	"cmp"
	"fmt"
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
}

// AnalyzeProject groups files into packages by their `package`
// declaration, analyses every package with the whole project in scope,
// and runs the project-wide rules. The Project is never nil.
func AnalyzeProject(files []*ast.File, opts Options) (*Project, []Diagnostic) {
	proj := &Project{Packages: map[string]*Package{}}
	groups := groupFilesByPackage(files)
	names := slices.Sorted(maps.Keys(groups))
	analyzers := make(map[string]*analyzer, len(groups))
	for _, name := range names {
		a := newAnalyzer(proj, opts)
		a.runDeclPhase(groups[name])
		proj.Packages[name] = a.pkg
		analyzers[name] = a
	}
	var diags []Diagnostic
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
	c.checkBasePathFormat()
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

// singlePackage returns the only package, else the unnamed one, else the
// first by name, else an empty package.
func (p *Project) singlePackage() *Package {
	if len(p.Packages) == 1 {
		for _, pkg := range p.Packages {
			return pkg
		}
	}
	if pkg := p.Packages[""]; pkg != nil {
		return pkg
	}
	names := slices.Sorted(maps.Keys(p.Packages))
	if len(names) > 0 {
		return p.Packages[names[0]]
	}
	return newPackage()
}

// groupFilesByPackage groups files by their `package` name. Files without
// a declaration join the only named package, or share the "" group when
// there is none or several.
func groupFilesByPackage(files []*ast.File) map[string][]*ast.File {
	groups := map[string][]*ast.File{}
	for _, f := range files {
		name := ""
		if f.Package != nil {
			name = f.Package.Name
		}
		groups[name] = append(groups[name], f)
	}
	if unnamed, ok := groups[""]; ok && len(groups) == 2 {
		for name, group := range groups {
			if name != "" {
				groups[name] = append(group, unnamed...)
				delete(groups, "")
			}
		}
	}
	return groups
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
