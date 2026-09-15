package golang

import (
	"fmt"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// reservedAliases are the identifiers the event template binds. A
// payload package whose DSL name is one of these is imported under a
// different alias, or the generated file would shadow the name.
var reservedAliases = map[string]bool{
	"craftevents": true,
}

// importSet accumulates the Go imports a generated event file needs,
// keyed by path so the same package is never imported twice, and keeps
// every alias distinct.
type importSet struct {
	crossPkg crossPkg
	byPath   map[string]string
	taken    map[string]string // alias → path
}

func newImportSet(crossPkg crossPkg) *importSet {
	return &importSet{crossPkg: crossPkg, byPath: map[string]string{}, taken: map[string]string{}}
}

func (s *importSet) add(imp extraImport) {
	if imp.Path == "" {
		return
	}
	if _, ok := s.byPath[imp.Path]; ok {
		return
	}
	s.byPath[imp.Path] = s.claim(imp.Alias, imp.Path)
}

// claim returns an alias for path that no other import in the file uses.
// A blank alias is left blank - it names an import the template spells
// literally, and those are the aliases everything else avoids.
func (s *importSet) claim(alias, path string) string {
	if alias == "" {
		return ""
	}
	candidate := alias
	for i := 2; reservedAliases[candidate] || (s.taken[candidate] != "" && s.taken[candidate] != path); i++ {
		candidate = fmt.Sprintf("%s%d", alias, i)
	}
	s.taken[candidate] = path
	return candidate
}

// aliasFor returns the alias an already-added path was imported under.
func (s *importSet) aliasFor(path string) string { return s.byPath[path] }

// sorted returns the accumulated imports in path order.
func (s *importSet) sorted() []extraImport {
	paths := make([]string, 0, len(s.byPath))
	for p := range s.byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]extraImport, 0, len(paths))
	for _, p := range paths {
		out = append(out, extraImport{Alias: s.byPath[p], Path: p})
	}
	return out
}

// payloadRefType renders a payload written as a type reference in the
// current package, pinning every import the reference reaches into.
func (s *importSet) payloadRefType(ref *ast.NamedTypeRef, typesImport string) string {
	alias, bare, extra, use := resolveTypeRef(ref, s.crossPkg)
	if use.LocalTypes {
		s.add(extraImport{Alias: "types", Path: typesImport})
	}
	s.add(extra)
	s.addRefImports(ref)
	if extra.Path != "" {
		alias = s.aliasFor(extra.Path)
	}
	return alias + "." + bare
}

// addRefImports pins every cross-package import a reference and its
// generic arguments reach into.
func (s *importSet) addRefImports(ref *ast.NamedTypeRef) {
	set := map[string]bool{}
	walkCrossPkgImports(&ast.TypeRef{Named: ref}, s.crossPkg, set)
	pathAlias := map[string]string{}
	for alias, path := range s.crossPkg {
		pathAlias[path] = alias
	}
	for path := range set {
		s.add(extraImport{Alias: pathAlias[path], Path: path})
	}
}
