// Type/decl LSP completions: project-wide decls + service/middleware/security/enum-value lookups.
package lsp

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func (s *Server) defaultEnumCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	f := fieldAtCursor(view, pos)
	if f == nil || f.Type == nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return nil
	}
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	e, ok := v.lookup(f.Type.Named.Name.String(), semantic.EnumDecls).(*ast.EnumDecl)
	if !ok {
		return nil
	}
	enumVals := e.EnumValues()
	out := make([]protocol.CompletionItem, 0, len(enumVals))
	for _, val := range enumVals {
		out = append(out, protocol.CompletionItem{
			Label:      val.Name,
			Kind:       protocol.CompletionItemKindEnumMember,
			Detail:     "value of enum " + e.Name,
			InsertText: val.Name,
		})
	}
	return out
}

// serviceNameCompletions lists the primary `service Name` declarations
// of the buffer's package - the only valid `extend service` targets,
// since extends resolve per package.
func (s *Server) serviceNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	pkg := v.proj.Packages[v.currentPackage()]
	if pkg == nil {
		return nil
	}
	return declItems(pkg, semantic.ServiceDecls, protocol.CompletionItemKindInterface, map[string]bool{})
}

// declItems returns one completion item per declaration of the selected
// kinds in pkg, skipping names already in seen and recording the rest.
func declItems(pkg *semantic.Package, kinds semantic.DeclKind, kind protocol.CompletionItemKind, seen map[string]bool) []protocol.CompletionItem {
	var out []protocol.CompletionItem
	for _, d := range pkg.Decls(kinds) {
		name := d.DeclName()
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, protocol.CompletionItem{
			Label:         name,
			Kind:          kind,
			Detail:        kindDetail(declKind(d), pkg.Name),
			Documentation: strings.Join(declDoc(d), "\n"),
			InsertText:    name,
		})
	}
	return out
}

// projectDeclItems lists every declaration of the selected kinds across
// the project, one item per name (the first package by name wins a
// duplicate), sorted by label.
func (s *Server) projectDeclItems(currentURI, currentSrc string, kinds semantic.DeclKind, kind protocol.CompletionItemKind) []protocol.CompletionItem {
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	seen := map[string]bool{}
	var out []protocol.CompletionItem
	for _, pkgName := range sortedKeys(v.proj.Packages) {
		out = append(out, declItems(v.proj.Packages[pkgName], kinds, kind, seen)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// declKind names d's kind the way the source spells it (`middleware`,
// `error <Category>`, `service`, ...).
func declKind(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return "type"
	case *ast.EnumDecl:
		return "enum"
	case *ast.ScalarDecl:
		return "scalar"
	case *ast.ErrorDecl:
		return "error " + v.Category
	case *ast.MiddlewareDecl:
		return "middleware"
	case *ast.ServiceDecl:
		return "service"
	}
	return ""
}

// kindDetail renders a completion detail as `kind (pkg)`, or just kind
// for an unnamed package.
func kindDetail(kind, pkg string) string {
	if pkg == "" {
		return kind
	}
	return kind + " (" + pkg + ")"
}

// securitySchemeCompletions returns one item per scheme declared
// under `openapi.securitySchemes` in the project's
// craftgo.design.yaml. Used for `@security(<scheme>, ...)` arg 1.
// When the manifest is not findable (e.g. the file is open outside
// any project root) or carries no schemes, the function returns nil
// and the completion popup falls through to the generic branch -
// no manifest is a permissive mode the codegen already supports, so
// we mirror that here.
//
// Detail surfaces the OpenAPI scheme `type` (`oauth2`, `http`, ...)
// so the user can pick by category at a glance; the scheme `Scheme`
// (`bearer`, `basic`) and `In` (`header`, `query`, `cookie`) hint at
// the sub-shape when present.
func (s *Server) securitySchemeCompletions(currentURI string) []protocol.CompletionItem {
	fsPath := uriToPath(currentURI)
	if fsPath == "" {
		return nil
	}
	cfg, _, _, err := config.Find(filepath.Dir(fsPath))
	if err != nil || cfg == nil || len(cfg.OpenAPI.SecuritySchemes) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(cfg.OpenAPI.SecuritySchemes))
	for name, scheme := range cfg.OpenAPI.SecuritySchemes {
		detail := scheme.Type
		switch {
		case scheme.Scheme != "":
			detail = scheme.Type + " " + scheme.Scheme
		case scheme.In != "" && scheme.Name != "":
			detail = scheme.Type + " (" + scheme.In + " " + scheme.Name + ")"
		}
		doc := protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: fmt.Sprintf("**`%s`** - %s security scheme.\n\nDeclared in `craftgo.design.yaml` under `openapi.securitySchemes.%s`.", name, scheme.Type, name),
		}
		out = append(out, protocol.CompletionItem{
			Label:         name,
			Kind:          protocol.CompletionItemKindEnumMember,
			Detail:        detail,
			Documentation: doc,
			InsertText:    name,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// middlewareNameCompletions lists every `middleware Name` declaration in
// the project, so an `@middlewares(...)` argument list shows the closed
// set the semantic resolver accepts. Function-kind items are the closest
// icon editors have for "a function the runtime calls".
func (s *Server) middlewareNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	return s.projectDeclItems(currentURI, currentSrc, semantic.MiddlewareDecls, protocol.CompletionItemKindFunction)
}

// errorNameCompletions lists every `error <Category> Name` declaration in
// the project, so an `@errors(...)` argument list shows the closed set of
// declared error types. Class-kind items match how the generated Go code
// surfaces an error (a `<Name>Err` struct).
func (s *Server) errorNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	return s.projectDeclItems(currentURI, currentSrc, semantic.ErrorDecls, protocol.CompletionItemKindClass)
}

// typeCompletionsProjectWide lists type-position completions: the
// built-in primitives and every project-wide declaration except errors.
func (s *Server) typeCompletionsProjectWide(currentURI, currentSrc string) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, sp := range prims.All() {
		if sp.Doc == "" {
			continue
		}
		items = append(items, protocol.CompletionItem{
			Label:  sp.Name,
			Kind:   protocol.CompletionItemKindKeyword,
			Detail: "built-in",
		})
	}
	items = append(items, s.declCompletions(currentURI, currentSrc, typePositionDecls)...)
	return items
}

// typePositionDecls is every declaration kind a type-position completion
// offers. Errors are left out: they are not referenceable as types, and
// `@errors(...)` has its own resolution path.
const typePositionDecls = semantic.AnyDecl &^ semantic.ErrorDecls

// declCompletions offers every declaration of the selected kinds across
// the project. A declaration in another package is offered as `pkg.Name`
// (label and inserted text) so picking it lands a complete reference;
// a same-package declaration keeps its bare name, since qualifying a
// self-reference is illegal. Every other package is added as a Module
// item inserting `pkg.`, so typing its first letter reaches the
// qualified path.
func (s *Server) declCompletions(currentURI, currentSrc string, kinds semantic.DeclKind) []protocol.CompletionItem {
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	currentPkg := v.currentPackage()
	var items []protocol.CompletionItem
	for _, pkgName := range sortedKeys(v.proj.Packages) {
		pkg := v.proj.Packages[pkgName]
		for _, d := range pkg.Decls(kinds) {
			label := d.DeclName()
			detail := declSummary(d)
			if pkg.Name != "" && pkg.Name != currentPkg {
				label = pkg.Name + "." + d.DeclName()
				detail = pkg.Name + " - " + detail
			}
			items = append(items, protocol.CompletionItem{
				Label:         label,
				Kind:          declSymbolKindToCompletion(d),
				Detail:        detail,
				Documentation: strings.Join(declDoc(d), "\n"),
				InsertText:    label,
			})
		}
	}
	for _, pkgName := range sortedKeys(v.proj.Packages) {
		if pkgName == "" || pkgName == currentPkg {
			continue
		}
		items = append(items, protocol.CompletionItem{
			Label:      pkgName,
			Kind:       protocol.CompletionItemKindModule,
			Detail:     "package",
			InsertText: pkgName + ".",
		})
	}
	return items
}

func declSymbolKindToCompletion(d ast.Decl) protocol.CompletionItemKind {
	switch d.(type) {
	case *ast.TypeDecl:
		return protocol.CompletionItemKindStruct
	case *ast.EnumDecl:
		return protocol.CompletionItemKindEnum
	case *ast.ErrorDecl:
		return protocol.CompletionItemKindStruct
	case *ast.ScalarDecl:
		return protocol.CompletionItemKindUnit
	case *ast.MiddlewareDecl:
		return protocol.CompletionItemKindFunction
	case *ast.ServiceDecl:
		return protocol.CompletionItemKindInterface
	}
	return protocol.CompletionItemKindClass
}
