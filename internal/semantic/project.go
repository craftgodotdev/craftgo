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
//      filesystem (when a root is known) and record metadata for the LSP.
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
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Project is the cross-package analysis result. Packages is keyed by
// the package's `package X` declaration name (the value of
// [Package.Name]), so files in any folder sharing the same name
// merge into a single entry. FileImports retains the per-file import
// metadata for LSP "go-to-definition" - at the analysis layer
// resolution uses package names directly, but the IDE benefits from
// knowing which folder each `import "path"` referred to.
type Project struct {
	// Root is the absolute design folder used for filesystem
	// validation of `import "path"`. Empty when AnalyzeProject was
	// called without [Options.DesignRoot] - in that case Packages
	// holds the same single-package result as [Analyze].
	Root string
	// Packages maps `package X` name → analysed [Package].
	Packages map[string]*Package
	// FileImports maps file path → alias → relative import path
	// (path is the value the user wrote in `import "path"`).
	FileImports map[string]map[string]string
}

// AnalyzeProject groups files into packages by their `package X`
// declaration, analyses every package with the whole project in scope,
// and runs the project-wide rules. The returned [Project] is always
// non-nil; consumers may inspect partial results even when diagnostics
// are reported.
func AnalyzeProject(files []*ast.File, opts Options) (*Project, []Diagnostic) {
	proj := &Project{
		Root:        opts.DesignRoot,
		Packages:    map[string]*Package{},
		FileImports: map[string]map[string]string{},
	}
	groups := groupFilesByPackage(files)
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	// The skip flags mute the per-package rules whose project-aware
	// twin runs below.
	perPkgOpts := opts
	perPkgOpts.skipBindingTypeCheckQualified = true
	perPkgOpts.skipPathParamCheck = true
	analyzers := make(map[string]*analyzer, len(groups))
	for _, name := range names {
		a := newAnalyzer(proj, perPkgOpts)
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
	r.checkProjectServiceUniqueness()
	r.checkProjectGroupChecks()
	r.checkProjectMiddlewareUniqueness()
	r.checkProjectFieldDefaults()
	r.checkProjectBindings()
	r.checkProjectDuplicateWireNames()
	r.checkProjectPathParams()
	r.checkProjectPathCollision()
	r.checkProjectOperationIDUniqueness()
	r.checkProjectAutoPathField()
	r.checkProjectBodyBindingVerb()
	r.checkProjectRequestBodyType()
	r.checkProjectFieldRules()
	r.checkProjectFieldGroups()
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

// filePos returns a representative position for diagnostics anchored
// at the file as a whole. Falls back to line 1 column 1 for files
// without a package decl. Test-only today, but kept on the package
// surface because project diagnostics anchored to a whole file (e.g.
// import-resolution, package-name conflicts) eventually need it.
func filePos(f *ast.File) lexer.Position {
	if f == nil {
		return lexer.Position{}
	}
	if f.Package != nil {
		return f.Package.Pos
	}
	for _, d := range f.Decls {
		return d.DeclPos()
	}
	return lexer.Position{Line: 1, Column: 1}
}

// fileFilename extracts the filename used to parse a file. Best-effort
// fallback to scanning the first decl when the package decl is
// missing, so synthetic ASTs (`hand-built in tests`) still land in a
// consistent group.
func fileFilename(f *ast.File) string {
	if f == nil {
		return ""
	}
	if f.Package != nil && f.Package.Pos.Filename != "" {
		return f.Package.Pos.Filename
	}
	for _, d := range f.Decls {
		if pos := d.DeclPos(); pos.Filename != "" {
			return pos.Filename
		}
	}
	return ""
}
