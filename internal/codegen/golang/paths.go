package golang

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/route"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// servicePackage returns the fallback Go package name derived from a service
// name ("UserService" -> "userservice"), used only when a service's DSL package
// has no name (a single-file design with no `package` declaration). Kept for
// the import-alias uniqueness suffix, which must stay per-service.
func servicePackage(svcName string) string { return strings.ToLower(svcName) }

// servicePkgName is the Go package declaration for a service's generated
// handler / transport / routes files. It reuses the DSL package name, so the
// handlers land in the same package identifier as their types (`project`
// service code alongside the `project` types), matching what the author wrote.
// Multiple services in one DSL package therefore share a package name across
// their separate directories - legal in Go, and the route hub imports them
// under distinct per-service aliases. Falls back to the service-derived name
// for an unnamed (single-file) package.
func servicePkgName(pkgName, svcName string) string {
	if pkgName == "" {
		return servicePackage(svcName)
	}
	return pkgName
}

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
// service's methods for the given group - [route.OutputSegment] under codegen's
// own name. The rule (a non-empty @group REPLACES the service-name segment)
// lives in the route package so the analyser's group-collision check compares
// exactly the directory this function hands the emitters; two services claiming
// one segment is rejected at analysis time as `service/group-collision`.
func outputSegFor(svcName, group, style string) string {
	return route.OutputSegment(svcName, group, style)
}

// serviceOutputDir returns projectRoot/output/<segment>, where the segment is
// the @group (replacing the service name) or the service directory when
// ungrouped. The single place per-method output directories are built so
// transport handlers, the per-group errors helper, and service stubs all land
// identically.
func serviceOutputDir(projectRoot, output, svcName, group, style string) string {
	return filepath.Join(projectRoot, output, filepath.FromSlash(outputSegFor(svcName, group, style)))
}

// consumersFileName is the file, at the root of the transport output,
// holding one service's consumer handler set - [route.ConsumersFileName]
// under codegen's own name, so the analyser's collision check compares the
// file the emitter writes.
func consumersFileName(svcName, style string) string {
	return route.ConsumersFileName(svcName, style) + ".go"
}

// eventsPkgImport is the Go import path of one service's event package -
// its publisher and its Consumers interface, which share a directory.
// outDir is the Go event target's output directory.
func eventsPkgImport(cfg *config.Config, outDir, svcName string) string {
	return goImportFromRel(cfg.LibraryPackage(), outDir) + "/" + outputSegFor(svcName, "", cfg.Output.FileCase)
}

// typesImportRoot is the Go import path the generated types tree sits at.
// Every types import is this root plus the DSL package name, so the rule
// is decided here once for the per-service import paths, the cross-package
// table and the event payload imports.
//
// Both it and [eventsPkgImport] name the contract half, which a
// projection shares with the design source rather than generating under
// its own module path.
func typesImportRoot(cfg *config.Config) string {
	return goImportFromRel(cfg.LibraryPackage(), cfg.Output.Types)
}

// importPathsForGroup computes the Go import paths for one service+group. A
// non-empty @group replaces the service-name segment on transport + service +
// routes alike; pkg.Name drives types. Routes are emitted one file per group
// (in the group's folder), so this group's routes path is the same segment as
// its transport and service folders.
func importPathsForGroup(cfg *config.Config, pkg *semantic.Package, svcName, group string) importPaths {
	seg := outputSegFor(svcName, group, cfg.Output.FileCase)
	return importPaths{
		Types:      typesImportRoot(cfg) + "/" + pkg.Name,
		Transport:  goImportFromRel(cfg.Package, cfg.Output.Transport) + "/" + seg,
		Routes:     goImportFromRel(cfg.Package, cfg.Output.Routes) + "/" + seg,
		Service:    goImportFromRel(cfg.Package, cfg.Output.Service) + "/" + seg,
		Svccontext: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
}

// methodGroups maps each of a service's method names to the @group that applies
// to it: the primary block's @group for primary methods, and each extend block's
// own @group for its methods - or, when an extend declares none, the primary's
// @group (see [effectiveGroup]). "" means ungrouped (files stay at the service
// root). Keyed by name (unique within a service) rather than pointer because
// later passes - generic monomorphisation, the OpenAPI builder - hand codegen
// cloned method values whose pointers no longer match the parsed block members.
func methodGroups(svc *semantic.ServiceInfo) map[string]string {
	return memberGroups(svc, func(d *ast.ServiceDecl) []string {
		out := make([]string, 0, len(d.Members))
		for _, m := range d.Methods() {
			out = append(out, m.Name)
		}
		return out
	})
}

// consumerGroups is [methodGroups] for a service's consumers.
func consumerGroups(svc *semantic.ServiceInfo) map[string]string {
	return memberGroups(svc, func(d *ast.ServiceDecl) []string {
		out := make([]string, 0, len(d.Members))
		for _, c := range d.Consumers() {
			out = append(out, c.Name)
		}
		return out
	})
}

// memberGroups maps each member name one block declares to the @group
// that applies to it. names extracts the member kind's names, so methods,
// events and consumers all read the same group rule.
func memberGroups(svc *semantic.ServiceInfo, names func(*ast.ServiceDecl) []string) map[string]string {
	out := map[string]string{}
	if svc == nil {
		return out
	}
	primaryGroup := route.ServiceGroup(svc.Primary)
	if svc.Primary != nil {
		for _, n := range names(svc.Primary) {
			out[n] = primaryGroup
		}
	}
	for _, e := range svc.Extends {
		g := route.EffectiveGroup(e, primaryGroup)
		for _, n := range names(e) {
			out[n] = g
		}
	}
	return out
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
	return idents.PascalCase(group)
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
