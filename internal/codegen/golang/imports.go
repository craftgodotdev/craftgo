package golang

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// goImport is one import of a generated file; an empty Alias imports the package under its own name.
type goImport struct {
	Alias string
	Path  string
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
)

// importSet collects the imports of one generated Go file and spells the types the file names:
// one alias per import path, distinct from the others, from the names the file's template binds
// and from the packages a builtin's Go type lives in.
type importSet struct {
	crossPkg crossPkg
	home     goImport
	reserved map[string]bool
	byPath   map[string]string // path → alias
	taken    map[string]string // alias → path
}

// newImportSet returns an empty set for a file whose template binds names. home is the package the
// file names under a fixed alias: the current DSL package's types, or a proto service's messages.
func newImportSet(crossPkg crossPkg, home goImport, names []string) *importSet {
	s := &importSet{crossPkg: crossPkg, home: home, reserved: map[string]bool{}, byPath: map[string]string{}, taken: map[string]string{}}
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

// goType spells t in Go, importing every package it reaches; an optional is a pointer unless the
// type already holds nil.
func (s *importSet) goType(t *ast.TypeRef) string {
	if t == nil {
		return ""
	}
	if t.Map != nil {
		return "map[" + s.goType(t.Map.Key) + "]" + s.goType(t.Map.Value)
	}
	depth := t.ArrayDepth
	if depth == 0 && t.Array {
		depth = 1
	}
	leaf := strings.Repeat("[]", depth) + s.named(t.Named)
	if t.Optional && !isNilableGoType(leaf) {
		leaf = "*" + leaf
	}
	return leaf
}

// named spells n in Go, importing every package it and its type arguments reach: a builtin as its
// Go type, a declared type through [importSet.qualify].
func (s *importSet) named(n *ast.NamedTypeRef) string {
	if n == nil || n.Name == nil {
		return ""
	}
	name := n.Name.String()
	if sp, ok := prims.Lookup(name); ok {
		if sp.GoImport != "" {
			// The package keeps its own name, which no other import can take.
			s.byPath[sp.GoImport] = ""
		}
		return sp.Go
	}
	out := s.qualify(name)
	if len(n.Args) == 0 {
		return out
	}
	args := make([]string, len(n.Args))
	for i, a := range n.Args {
		args[i] = s.goType(a)
	}
	return out + "[" + strings.Join(args, ", ") + "]"
}

// qualify spells a declared type's name: a bare name under the home package's alias, a qualified
// one (`shared.ID`) under the alias its package's import takes. A package the set cannot import
// keeps the name as written.
func (s *importSet) qualify(name string) string {
	pkgName, sym, qualified := strings.Cut(name, ".")
	if !qualified {
		return s.add(s.home.Alias, s.home.Path) + "." + name
	}
	path, ok := s.crossPkg[pkgName]
	if !ok {
		return name
	}
	return s.add(pkgName, path) + "." + sym
}
