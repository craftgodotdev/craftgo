package codegen

import (
	"path"
	"path/filepath"
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
func ServiceDir(svcName string) string { return kebabCase(svcName) }

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

// servicePrefix returns the path prefix declared on a service via the
// @prefix("/...") decorator, or the empty string when absent.
func servicePrefix(svc *ast.ServiceDecl) string {
	if svc == nil {
		return ""
	}
	for _, d := range svc.Decorators {
		if d.Name != "prefix" || len(d.Args) == 0 {
			continue
		}
		if s, ok := d.Args[0].Value.(*ast.StringLit); ok {
			return s.Value
		}
	}
	return ""
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

// methodFullPath joins the OpenAPI base path, the service prefix, and the
// method's own path into a single absolute route. Empty segments are dropped;
// consecutive slashes are collapsed; the result always begins with '/'.
// @group is deliberately absent - it nests generated files on disk, not the
// URL.
//
// When the method declares no inline path the fallback is the method name
// in kebab-case ("Ping" → "/ping"). This avoids collisions when several
// pathless methods share the same service prefix.
func methodFullPath(basePath string, svc *ast.ServiceDecl, m *ast.Method) string {
	parts := []string{}
	if basePath != "" {
		parts = append(parts, basePath)
	}
	if p := servicePrefix(svc); p != "" {
		parts = append(parts, p)
	}
	if m.Path != nil {
		parts = append(parts, semantic.PathString(m.Path))
	} else {
		parts = append(parts, "/"+kebabCase(m.Name))
	}
	joined := strings.Join(parts, "/")
	for strings.Contains(joined, "//") {
		joined = strings.ReplaceAll(joined, "//", "/")
	}
	if joined == "" || joined[0] != '/' {
		joined = "/" + joined
	}
	if len(joined) > 1 {
		joined = strings.TrimRight(joined, "/")
	}
	return joined
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

// serviceGroupSegOf returns the @group slash path for a service in pkg, or ""
// when the service is unknown or ungrouped. Centralises the lookup so import
// paths and on-disk directories nest identically.
func serviceGroupSegOf(pkg *semantic.Package, svcName string) string {
	si := pkg.Services[svcName]
	if si == nil {
		return ""
	}
	return serviceGroup(si.Primary)
}

// groupedSeg appends a service's @group path to its base directory segment,
// e.g. "user-service" + "admin/ops" → "user-service/admin/ops". Returns base
// unchanged when the service has no @group. The result is a forward-slash
// path suitable for a Go import.
func groupedSeg(base, group string) string {
	if group == "" {
		return base
	}
	return base + "/" + group
}

// serviceOutputDir returns projectRoot/output/<service>[/<group>], converting
// the group to OS path separators. The single place per-service output
// directories are built so transport handlers, the errors helper, and service
// stubs all nest the @group identically.
func serviceOutputDir(projectRoot, output, svcName, group string) string {
	dir := filepath.Join(projectRoot, output, ServiceDir(svcName))
	if group != "" {
		dir = filepath.Join(dir, filepath.FromSlash(group))
	}
	return dir
}

// importPathsFor computes the per-service Go import paths for a project.
// pkg.Name is appended to the types output; the kebab-case service directory
// name is appended to transport / routes / service. A service's @group nests
// transport and service one level deeper (under <service>/<group>); routes
// stay flat so the per-service route file remains the single registration hub.
func importPathsFor(cfg *config.Config, pkg *semantic.Package, svcName string) importPaths {
	svcSeg := ServiceDir(svcName)
	grouped := groupedSeg(svcSeg, serviceGroupSegOf(pkg, svcName))
	return importPaths{
		Types:      goImportFromRel(cfg.Package, cfg.Output.Types) + "/" + pkg.Name,
		Transport:  goImportFromRel(cfg.Package, cfg.Output.Transport) + "/" + grouped,
		Routes:     goImportFromRel(cfg.Package, cfg.Output.Routes) + "/" + svcSeg,
		Service:    goImportFromRel(cfg.Package, cfg.Output.Service) + "/" + grouped,
		Svccontext: goImportFromRel(cfg.Package, fileDirRel(cfg.Output.Svccontext)),
	}
}

// hasBodyVerb reports whether the given HTTP verb conventionally carries a
// request body. The handler generator only emits JSON-decode scaffolding
// for body-bearing verbs.
func hasBodyVerb(verb string) bool {
	switch strings.ToUpper(verb) {
	case "POST", "PUT", "PATCH":
		return true
	}
	return false
}

// kebabCase splits a PascalCase / camelCase identifier into its component
// words and joins them with hyphens. `GetUser` → `get-user`, `HTTPRequest`
// → `http-request`. Used for generated filenames so directory listings
// stay readable on case-sensitive filesystems.
func kebabCase(s string) string {
	return idents.KebabCase(s)
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
