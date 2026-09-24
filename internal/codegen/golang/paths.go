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

// servicePackage lower-cases a service name ("UserService" -> "userservice").
func servicePackage(svcName string) string { return strings.ToLower(svcName) }

// servicePkgName is the Go package name of a service's handler, logic and routes files: the DSL
// package's name, or servicePackage for an unnamed package.
func servicePkgName(pkgName, svcName string) string {
	if pkgName == "" {
		return servicePackage(svcName)
	}
	return pkgName
}

// goImportFromRel turns a project-relative directory ("./internal/handler") into its import
// path under modulePath.
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

// fileDirRel returns the forward-slash directory of a project-relative file path, "" for a root file.
func fileDirRel(filePath string) string {
	filePath = strings.ReplaceAll(filePath, "\\", "/")
	dir := path.Dir(filePath)
	if dir == "." {
		return ""
	}
	return dir
}

func httpVerb(verb string) string { return strings.ToUpper(verb) }

// importPaths are the import paths of one service segment's generated packages.
type importPaths struct {
	Types      string
	Transport  string
	Routes     string
	Service    string
	Svccontext string
}

// outputSegFor is [route.OutputSegment]: the @group, or the service's directory when ungrouped.
func outputSegFor(svcName, group, style string) string {
	return route.OutputSegment(svcName, group, style)
}

// serviceOutputDir returns projectRoot/output/<segment> for the service's group.
func serviceOutputDir(projectRoot, output, svcName, group, style string) string {
	return filepath.Join(projectRoot, output, filepath.FromSlash(outputSegFor(svcName, group, style)))
}

// typesImportRoot is the import path of output.types; a DSL package's types sit at root/<package>.
func typesImportRoot(cfg *config.Config) string {
	return goImportFromRel(cfg.Package, cfg.Output.Types)
}

// importPathsForGroup returns the import paths of the service's group segment; types follow pkg.Name.
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

// methodGroups maps each method name to its block's [route.EffectiveGroup], "" when ungrouped.
func methodGroups(svc *semantic.ServiceInfo) map[string]string {
	return memberGroups(svc, func(d *ast.ServiceDecl) []string {
		out := make([]string, 0, len(d.Members))
		for _, m := range d.Methods() {
			out = append(out, m.Name)
		}
		return out
	})
}

// memberGroups maps each name that names returns for a block to the block's effective @group.
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

// distinctGroups returns the @groups the service's methods use, sorted, "" first.
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

// groupAliasSuffix is the import-alias suffix of a @group ("admin/ops" → "AdminOps", "" → "").
func groupAliasSuffix(group string) string {
	return idents.PascalCase(group)
}

// transportAlias is a routes file's alias for a group's transport package ("transport", "transportV2").
func transportAlias(group string) string {
	return "transport" + groupAliasSuffix(group)
}

// renderDoc renders doc as indented `//` comment lines, "" for none.
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
