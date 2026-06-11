package codegen

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// ServicePackage returns the Go-identifier package name for a service.
// Service names use PascalCase in the DSL ("UserService"); the matching
// Go package declaration is the lowercase concatenation
// ("userservice") because Go identifiers cannot contain hyphens.
func ServicePackage(svcName string) string { return strings.ToLower(svcName) }

// ServiceDir returns the kebab-case directory name for a service. Used
// for filesystem paths and import segments - `UserService` becomes
// `user-service`. The Go package declaration inside the directory still
// uses [ServicePackage] (no hyphens) so the source remains compilable.
func ServiceDir(svcName string) string { return idents.KebabCase(svcName) }

// goImportFromRel converts a project-relative directory like
// "./internal/handler" into the Go import path "<modulePath>/internal/handler".
// Leading "./" is stripped, backslashes are normalised to forward slashes,
// and a trailing slash is removed.
func goImportFromRel(modulePath, rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.TrimPrefix(rel, "/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		return modulePath
	}
	return modulePath + "/" + rel
}

// fileDirRel returns the directory portion of a file path expressed in
// project-relative form (always forward-slash). Used for `output.svccontext`
// where the value points at a file rather than a directory.
func fileDirRel(filePath string) string {
	filePath = strings.ReplaceAll(filePath, "\\", "/")
	dir := path.Dir(filePath)
	if dir == "." {
		return ""
	}
	return dir
}

// serviceGroup returns the cleaned slash-delimited path derived from the
// service's @group("a/b/c") decorator. The group nests a service's generated
// transport handlers and service stubs under <service>/<group>/ - it does not
// appear in the HTTP route or the OpenAPI path. Returns "" when the decorator
// is absent or its value cleans to nothing. The value is trimmed of leading /
// trailing slashes and collapsed empty segments; the semantic phase rejects
// traversal ("..") and absolute forms before codegen runs.
func serviceGroup(svc *ast.ServiceDecl) string {
	if svc == nil {
		return ""
	}
	for _, d := range svc.Decorators {
		if d.Name != "group" || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return cleanGroupPath(s.Value)
		}
	}
	return ""
}

// cleanGroupPath normalises a @group value into a relative slash path: it
// trims surrounding slashes and drops empty segments so "/admin/" and
// "admin//ops" become "admin" and "admin/ops". Traversal ("." / "..") segments
// are dropped as a defence-in-depth backstop - the semantic phase rejects them
// outright - so a malformed value reaching codegen can only ever nest inside
// the service directory, never escape the output tree.
func cleanGroupPath(raw string) string {
	segs := strings.Split(raw, "/")
	kept := segs[:0]
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			continue
		}
		kept = append(kept, s)
	}
	return strings.Join(kept, "/")
}

// httpVerb maps DSL verb keywords to canonical HTTP method strings used in
// `http.ServeMux` patterns ("GET", "POST", ...).
func httpVerb(verb string) string { return strings.ToUpper(verb) }

// importPaths bundles every Go import path used by the transport / routes /
// service generators for a given project + service. Computed once per service.
type importPaths struct {
	Types      string
	Transport  string
	Routes     string
	Service    string
	Svccontext string
}

// outputSegFor returns the path segment, under an output base, that holds a
// service's methods for the given group. A non-empty @group REPLACES the
// service-name segment entirely (so `@group("v2")` on any service emits to
// `<base>/v2/`), giving the author full control of the layout; the ungrouped
// case falls back to the kebab-case service directory. The result is a
// forward-slash path - the group may itself be nested ("admin/ops").
//
// Because the group replaces the service name, it is effectively a global
// namespace: two services that pick the same group land in the same directory
// (and Go package). Keep groups unique per service - embed the service name in
// the group when in doubt.
func outputSegFor(svcName, group string) string {
	if group != "" {
		return group
	}
	return ServiceDir(svcName)
}

// serviceOutputDir returns projectRoot/output/<segment>, where the segment is
// the @group (replacing the service name) or the service directory when
// ungrouped. The single place per-method output directories are built so
// transport handlers, the per-group errors helper, and service stubs all land
// identically.
func serviceOutputDir(projectRoot, output, svcName, group string) string {
	return filepath.Join(projectRoot, output, filepath.FromSlash(outputSegFor(svcName, group)))
}

// importPathsForGroup computes the Go import paths for one service+group. A
// non-empty @group replaces the service-name segment on transport + service +
// routes alike; pkg.Name drives types. Routes are emitted one file per group
// (in the group's folder), so this group's routes path is the same segment as
// its transport and service folders.
func importPathsForGroup(cfg *config.Config, pkg *semantic.Package, svcName, group string) importPaths {
	seg := outputSegFor(svcName, group)
	return importPaths{
		Types:      goImportFromRel(cfg.Package, cfg.Output.Types) + "/" + pkg.Name,
		Transport:  goImportFromRel(cfg.Package, cfg.Output.Transport) + "/" + seg,
		Routes:     goImportFromRel(cfg.Package, cfg.Output.Routes) + "/" + seg,
		Service:    goImportFromRel(cfg.Package, cfg.Output.Service) + "/" + seg,
		Svccontext: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
}

// effectiveGroup returns the @group that applies to a service block. The block's
// own @group wins; an extend block that declares none inherits the primary
// block's @group, so `@group("admin")` on the service covers its extend blocks
// too unless an extend explicitly overrides with its own @group. Pass the
// primary block's group as primaryGroup (it is its own effective group).
func effectiveGroup(block *ast.ServiceDecl, primaryGroup string) string {
	if g := serviceGroup(block); g != "" {
		return g
	}
	return primaryGroup
}

// methodGroups maps each of a service's method names to the @group that applies
// to it: the primary block's @group for primary methods, and each extend block's
// own @group for its methods - or, when an extend declares none, the primary's
// @group (see [effectiveGroup]). "" means ungrouped (files stay at the service
// root). Keyed by name (unique within a service) rather than pointer because
// later passes - generic monomorphisation, the OpenAPI builder - hand codegen
// cloned method values whose pointers no longer match the parsed block members.
func methodGroups(svc *semantic.ServiceInfo) map[string]string {
	out := map[string]string{}
	if svc == nil {
		return out
	}
	primaryGroup := serviceGroup(svc.Primary)
	if svc.Primary != nil {
		for _, m := range svc.Primary.Methods() {
			out[m.Name] = primaryGroup
		}
	}
	for _, e := range svc.Extends {
		g := effectiveGroup(e, primaryGroup)
		for _, m := range e.Methods() {
			out[m.Name] = g
		}
	}
	return out
}

// methodGroupOf returns the @group of the block that declared m (primary or an
// extend), or "" when ungrouped / not found. The map-free form for callers
// that need one method's group without building the whole table.
func methodGroupOf(svc *semantic.ServiceInfo, m *ast.Method) string {
	if svc == nil || m == nil {
		return ""
	}
	primaryGroup := serviceGroup(svc.Primary)
	if svc.Primary != nil {
		for _, pm := range svc.Primary.Methods() {
			if pm.Name == m.Name {
				return primaryGroup
			}
		}
	}
	for _, e := range svc.Extends {
		for _, em := range e.Methods() {
			if em.Name == m.Name {
				return effectiveGroup(e, primaryGroup)
			}
		}
	}
	return ""
}

// distinctGroups returns the service's group set in deterministic order, with
// the empty (ungrouped) group sorted first. Used to know which group folders
// exist - one transport import + one errors helper per entry.
func distinctGroups(svc *semantic.ServiceInfo) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range methodGroups(svc) {
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// groupAliasSuffix is the PascalCased join of a @group's path segments
// ("admin/ops" → "AdminOps"), or "" for the ungrouped case. Import aliases that
// must stay distinct per group append it to a stable base.
func groupAliasSuffix(group string) string {
	var b strings.Builder
	for seg := range strings.SplitSeq(group, "/") {
		if seg == "" {
			continue
		}
		b.WriteString(pascalCase(seg))
	}
	return b.String()
}

// transportAlias derives the Go import alias a service's routes file uses for
// one group's transport package. The ungrouped package keeps the bare
// "transport" name; a grouped package appends the PascalCased group segments
// ("v2" → "transportV2", "admin/ops" → "transportAdminOps") so several group
// imports coexist without colliding.
func transportAlias(group string) string {
	return "transport" + groupAliasSuffix(group)
}

// renderDoc returns the user's leading `//` comments verbatim, with the
// same `//` prefix added back. Each line becomes its own Go-level
// comment line. `indent` is prepended to every emitted line so
// field-level comments stay inside the struct body. Returns "" for an
// empty doc slice so callers can concatenate unconditionally.
func renderDoc(doc []string, indent string) string {
	if len(doc) == 0 {
		return ""
	}
	lines := make([]string, len(doc))
	for i, line := range doc {
		lines[i] = indent + "// " + line + "\n"
	}
	return strings.Join(lines, "")
}
