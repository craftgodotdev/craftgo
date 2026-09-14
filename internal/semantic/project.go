package semantic

// Multi-package project analysis. AnalyzeProject groups files by
// their `package X` declaration - files anywhere under the design
// root that share an X declaration merge into one logical package,
// while files declaring different package names form separate
// packages. This matches the README's §"Imports" intent while also
// preserving the existing fixtures' "import = pull files in this
// folder into my package" behaviour: when files in different folders
// happen to declare the same package name, they merge.
//
// Lifecycle:
//
//   1. Parse every file (caller's responsibility).
//   2. Group files by their `f.Package.Name`. Files lacking a
//      package decl join the only named package, or form a group
//      keyed "" when there is none or several.
//   3. Build every package's symbol tables, then run the per-package
//      rule phases with the whole project in scope.
//   4. For each file, validate `import "path"` against the design
//      filesystem (when a root is known).
//   5. Walk every NamedTypeRef across every file; multi-part names
//      `pkg.Type` resolve directly to the Package whose pkg.Name ==
//      `pkg`. The DSL keeps no alias-based indirection - `import
//      alias "path"` is parsed but the alias is informational only.
//   6. Run the project-wide rules (cross-package uniqueness, path and
//      operationId collisions, group layout).
//
// Codes specific to this layer:
//
//   - [CodeImportUnresolved]      - `import "path"` doesn't exist
//     under the design root.
//   - [CodeImportEscape]          - path uses `..` / leading `/`.
//   - [CodeImportSelf]            - file imports its own folder
//     while declaring a package name that already covers it.
//   - [CodeRefUnknownPackage]     - `pkg.Type` references a package
//     name not declared anywhere in the project.
//   - [CodeRefUnknownSymbol]      - package resolves but the target
//     doesn't declare the named type.

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
)

// Project is the cross-package analysis result. Packages is keyed by
// the package's `package X` declaration name (the value of
// [Package.Name]), so files in any folder sharing the same name
// merge into a single entry.
type Project struct {
	// Root is the absolute design folder used for filesystem
	// validation of `import "path"`. Empty when AnalyzeProject was
	// called without [Options.DesignRoot].
	Root string
	// Packages maps `package X` name → analysed [Package].
	Packages map[string]*Package
}

// AnalyzeProject groups files into packages by their `package X`
// declaration, analyses every package with the whole project in scope,
// and runs the project-wide rules. The returned [Project] is always
// non-nil; consumers may inspect partial results even when diagnostics
// are reported.
func AnalyzeProject(files []*ast.File, opts Options) (*Project, []Diagnostic) {
	proj := &Project{
		Root:     opts.DesignRoot,
		Packages: map[string]*Package{},
	}
	groups := groupFilesByPackage(files)
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
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
	r := &refResolver{proj: proj, diags: diags, basePath: opts.BasePath, fileCase: resolvedFileCase(opts.FileCase)}
	for _, f := range files {
		r.processFile(f, opts.DesignRoot)
	}
	r.checkProjectGroupChecks()
	r.checkProjectMiddlewareUniqueness()
	r.checkProjectPathCollision()
	r.checkProjectOperationIDUniqueness()
	r.checkProjectEvents()
	return proj, r.diags
}

// singlePackage returns the package a single-package analysis produced:
// the only named package, or the unnamed bucket when no file declares a
// package. Falls back to the first package by name.
func (p *Project) singlePackage() *Package {
	if len(p.Packages) == 1 {
		for _, pkg := range p.Packages {
			return pkg
		}
	}
	if pkg := p.Packages[""]; pkg != nil {
		return pkg
	}
	names := make([]string, 0, len(p.Packages))
	for name := range p.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		return p.Packages[names[0]]
	}
	return newAnalyzer(p, Options{}).pkg
}

// groupFilesByPackage classifies every file by its `package X`
// declaration. Files with no decl share the bucket "" - the same
// loose policy [analyzer.checkPackageName] uses for single-package
// analysis. Each returned group becomes one [Package] in the
// resulting [Project].
func groupFilesByPackage(files []*ast.File) map[string][]*ast.File {
	groups := map[string][]*ast.File{}
	for _, f := range files {
		name := ""
		if f.Package != nil {
			name = f.Package.Name
		}
		groups[name] = append(groups[name], f)
	}
	// Files without a package declaration belong to the project's only
	// named package when there is exactly one.
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

// folderExists reports whether path (relative to designRoot) maps to
// a directory containing at least one .craftgo file. Used to validate
// `import "path"` directives - the import is informational in the
// new package-name-keyed model, but a typo is still worth flagging.
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
