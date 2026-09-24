package semantic

import (
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
)

// Project is the analysis of a whole design. Packages is keyed by
// [Package.Name]; files in any folder that declare one name share an entry.
type Project struct {
	// Root is [Options.DesignRoot].
	Root     string
	Packages map[string]*Package
}

// AnalyzeProject groups files into packages by their `package`
// declaration, analyses every package with the whole project in scope,
// and runs the project-wide rules. The Project is never nil.
func AnalyzeProject(files []*ast.File, opts Options) (*Project, []Diagnostic) {
	proj := &Project{
		Root:     opts.DesignRoot,
		Packages: map[string]*Package{},
	}
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
	r := &refResolver{proj: proj, diags: diags, basePath: opts.BasePath, fileCase: opts.FileCase}
	for _, f := range files {
		r.processFile(f, opts.DesignRoot)
	}
	r.checkProjectGroupChecks()
	r.checkProjectMiddlewareUniqueness()
	r.checkProjectPathCollision()
	r.checkProjectOperationIDUniqueness()
	r.checkProjectEvents()
	sortDiagnostics(r.diags)
	return proj, r.diags
}

// sortDiagnostics orders diags by file, offset, code and message.
func sortDiagnostics(diags []Diagnostic) {
	slices.SortStableFunc(diags, func(a, b Diagnostic) int {
		return cmp.Or(
			cmp.Compare(a.Pos.Filename, b.Pos.Filename),
			cmp.Compare(a.Pos.Offset, b.Pos.Offset),
			cmp.Compare(a.Code, b.Code),
			cmp.Compare(a.Msg, b.Msg),
		)
	})
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
	return newAnalyzer(p, Options{}).pkg
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
