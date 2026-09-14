package golang

import (
	"fmt"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// reservedAliases are the identifiers the event templates bind: their
// imports, their parameters, and the variables inside their bodies. A
// payload package whose DSL name is one of these is imported under a
// different alias, or the generated file would shadow the name.
var reservedAliases = map[string]bool{
	// package-level imports
	"craftevents": true,
	"context":     true,
	"errors":      true,
	"fmt":         true,
	// parameters, receivers and locals the templates bind
	"bus":     true,
	"h":       true,
	"chain":   true,
	"groups":  true,
	"g":       true,
	"own":     true,
	"ctx":     true,
	"payload": true,
	"err":     true,
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

// payloadType renders a resolved event's payload from the consuming
// package's point of view. A contract declared elsewhere renders through
// its own package's alias, so a consumer never needs the publisher's
// `types` alias.
func (s *importSet) payloadType(ev semantic.ResolvedEvent, typesImport string, cfg *config.Config) string {
	if ev.PayloadName == "" {
		return ""
	}
	if path := s.payloadImport(ev, typesImport, cfg); path != "" {
		s.add(extraImport{Alias: ev.PayloadPkg, Path: path})
		return s.aliasFor(path) + "." + ev.PayloadName
	}
	s.add(extraImport{Alias: "types", Path: typesImport})
	return "types." + ev.PayloadName
}

// payloadImport returns the Go import path a payload's package lives at,
// or "" when it is the consuming package's own types import. Cross-
// package paths come from [crossPkg]; a payload in a package the
// resolver did not see falls back to the same `<types>/<pkg>` rule
// [importPathsForGroup] applies.
func (s *importSet) payloadImport(ev semantic.ResolvedEvent, typesImport string, cfg *config.Config) string {
	if path, ok := s.crossPkg[ev.PayloadPkg]; ok {
		return path
	}
	if ev.PayloadPkg == "" || typesImport == "" || strings.HasSuffix(typesImport, "/"+ev.PayloadPkg) {
		return ""
	}
	return typesImportRoot(cfg) + "/" + ev.PayloadPkg
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
