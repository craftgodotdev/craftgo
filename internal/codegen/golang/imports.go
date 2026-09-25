package golang

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/strfmt"
)

// goImport is one import of a generated file; an empty Alias imports the package under its own name.
type goImport struct {
	Alias string
	Path  string
}

// Spec returns imp as an import spec: the quoted path, after the alias when the alias is not the
// package's own name.
func (imp goImport) Spec() string {
	if imp.Alias == "" || imp.Alias == path.Base(imp.Path) {
		return strconv.Quote(imp.Path)
	}
	return imp.Alias + " " + strconv.Quote(imp.Path)
}

// localAlias is the alias a file imports its own DSL package's types under.
const localAlias = "types"

// The names each template binds where it writes an imported package's name.
var (
	transportNames  = []string{"http", "strconv", "server", "service", "svccontext", "svcCtx", "w", "r", "req", "c", "err", "_q", "_v", "_vs", "_w"}
	serviceNames    = []string{"context", "http", "log", "grpc", "svccontext"}
	eventsNames     = []string{"craftevents", "fmt"}
	grpcMethodNames = []string{"context", "rpc", "grpc", "service"}
	wiringGRPCNames = []string{"context", "rpc", "svccontext", "ctx", "srv", "svcCtx"}
	routesNames     = []string{"time", "server", "svccontext", "srv", "svcCtx"}
	typesNames      = []string{"wire"}
	errorsNames     = []string{"json", "http", "strconv", "wire", "e", "w"}
	validateNames   = append([]string{"fmt", "regexp", "utf8", "reflect", "v", "item", "seen"}, formatPackages()...)
)

// formatPackages returns the names of the packages a @format check imports.
func formatPackages() []string {
	var out []string
	for _, sp := range strfmt.All {
		for _, imp := range sp.Imports {
			out = append(out, path.Base(imp))
		}
	}
	return out
}

// importSet collects the imports of one generated Go file and spells the types the file names:
// one alias per import path, distinct from the others, from the names the file's template binds
// and from the packages a builtin's Go type lives in.
type importSet struct {
	crossPkg crossPkg
	res      *semantic.Resolver
	home     goImport
	reserved map[string]bool
	byPath   map[string]string // path → alias
	taken    map[string]string // alias → path
}

// newImportSet returns an empty set for a file whose template binds names. r resolves the DSL types
// the file names, nil for a file that names none; home is the package the file names under a fixed
// alias: the current DSL package's types, or a proto service's messages. A file of the DSL package's
// own types has no home and names that package's types bare.
func newImportSet(r *projectResolver, home goImport, names []string) *importSet {
	s := &importSet{home: home, reserved: map[string]bool{}, byPath: map[string]string{}, taken: map[string]string{}}
	if r != nil {
		s.crossPkg, s.res = r.CrossPkg, r.Resolver
	}
	for _, n := range names {
		s.reserved[n] = true
	}
	for _, sp := range prims.All() {
		if sp.GoImport != "" {
			s.reserved[path.Base(sp.GoImport)] = true
		}
	}
	if home.Path != "" {
		s.taken[home.Alias] = home.Path
	}
	return s
}

// add imports path and returns its alias: alias itself, or alias2, alias3, ... when alias is bound.
func (s *importSet) add(alias, path string) string {
	if a, ok := s.byPath[path]; ok {
		return a
	}
	candidate := alias
	for i := 2; s.reserved[candidate] || (s.taken[candidate] != "" && s.taken[candidate] != path); i++ {
		candidate = fmt.Sprintf("%s%d", alias, i)
	}
	s.taken[candidate] = path
	s.byPath[path] = candidate
	return candidate
}

// use imports path under its package's own name, which the set's reserved names keep free.
func (s *importSet) use(path string) {
	s.byPath[path] = ""
}

// has reports whether path is imported.
func (s *importSet) has(path string) bool {
	_, ok := s.byPath[path]
	return ok
}

// imports returns the imports in path order.
func (s *importSet) imports() []goImport {
	out := make([]goImport, 0, len(s.byPath))
	for _, p := range slices.Sorted(maps.Keys(s.byPath)) {
		out = append(out, goImport{Alias: s.byPath[p], Path: p})
	}
	return out
}

// scratch returns a copy of s whose imports never reach s, to spell a type the file names only
// in a comment.
func (s *importSet) scratch() *importSet {
	c := *s
	c.byPath, c.taken = maps.Clone(s.byPath), maps.Clone(s.taken)
	return &c
}

// goType spells t as the file names it, a declared type through [importSet.qualify], and imports
// every package t reaches.
func (s *importSet) goType(t *ast.TypeRef) string {
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) { s.importBuiltin(n.Name.String()) })
	return goType(t, s.res, s.qualify)
}

// named is [importSet.goType] for a named type.
func (s *importSet) named(n *ast.NamedTypeRef) string {
	n.WalkNamedRefs(func(n *ast.NamedTypeRef) { s.importBuiltin(n.Name.String()) })
	return goNamedType(n, s.res, s.qualify)
}

// importBuiltin imports the package the Go type of builtin name lives in, if it has one.
func (s *importSet) importBuiltin(name string) {
	if sp, ok := prims.Lookup(name); ok && sp.GoImport != "" {
		s.use(sp.GoImport)
	}
}

// qualify spells a declared type's name: a bare name under the home package's alias, or bare
// without a home, a qualified one (`shared.ID`) under the alias its package's import takes. A
// package the set cannot import keeps the name as written.
func (s *importSet) qualify(name string) string {
	pkgName, sym, qualified := strings.Cut(name, ".")
	if !qualified {
		if s.home.Path == "" {
			return name
		}
		return s.add(s.home.Alias, s.home.Path) + "." + name
	}
	path, ok := s.crossPkg[pkgName]
	if !ok {
		return name
	}
	return s.add(pkgName, path) + "." + sym
}
