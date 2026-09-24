package golang

import (
	"fmt"
	"sort"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// reservedAliases are the identifiers events.tmpl binds; a payload package named like one is
// imported under another alias.
var reservedAliases = map[string]bool{
	"craftevents": true,
}

// grpcReservedAliases are the identifiers the gRPC templates bind.
var grpcReservedAliases = map[string]bool{
	pbAlias: true, "service": true, "svccontext": true, "rpc": true, "grpc": true,
	"context": true, "log": true,
}

// importSet collects a generated file's imports, one per path, under aliases distinct from each
// other and from the template's reserved names.
type importSet struct {
	crossPkg crossPkg
	reserved map[string]bool
	byPath   map[string]string
	taken    map[string]string // alias → path
}

// newImportSet is the set of an event file, resolving DSL type
// references through crossPkg.
func newImportSet(crossPkg crossPkg) *importSet {
	return &importSet{crossPkg: crossPkg, reserved: reservedAliases, byPath: map[string]string{}, taken: map[string]string{}}
}

// newGRPCImportSet is the set of a gRPC file: pb packages only, no DSL
// types.
func newGRPCImportSet() *importSet {
	return &importSet{reserved: grpcReservedAliases, byPath: map[string]string{}, taken: map[string]string{}}
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

// claim returns an alias for path that no other import in the file uses; a blank alias stays blank.
func (s *importSet) claim(alias, path string) string {
	if alias == "" {
		return ""
	}
	candidate := alias
	for i := 2; s.reserved[candidate] || (s.taken[candidate] != "" && s.taken[candidate] != path); i++ {
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

// payloadRefType renders ref as written in the current package and adds every import it reaches.
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

// addRefImports adds every cross-package import ref and its generic arguments reach.
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
