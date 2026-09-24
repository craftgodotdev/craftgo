package lsp

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// defaultValueCompletions answers `@default(|)` from the field's type: an
// enum's values, or true and false for a bool or a scalar over one; else nil.
func (s *Server) defaultValueCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	f := fieldAtCursor(view, pos)
	if f == nil || f.Type == nil || f.Type.Map != nil || f.Type.Array || f.Type.Named == nil || f.Type.Named.Name == nil {
		return nil
	}
	name := f.Type.Named.Name.String()
	if sp, ok := prims.Lookup(name); ok {
		if sp.Kind == prims.Bool {
			return boolLiteralCompletions()
		}
		return nil
	}
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	switch d := v.lookup(name, semantic.EnumDecls|semantic.ScalarDecls).(type) {
	case *ast.EnumDecl:
		enumVals := d.EnumValues()
		out := make([]protocol.CompletionItem, 0, len(enumVals))
		for _, val := range enumVals {
			out = append(out, protocol.CompletionItem{
				Label:      val.Name,
				Kind:       protocol.CompletionItemKindEnumMember,
				Detail:     "value of enum " + d.Name,
				InsertText: val.Name,
			})
		}
		return out
	case *ast.ScalarDecl:
		if sp, ok := prims.Lookup(d.Primitive); ok && sp.Kind == prims.Bool {
			return boolLiteralCompletions()
		}
	}
	return nil
}

// boolLiteralCompletions offers true and false.
func boolLiteralCompletions() []protocol.CompletionItem {
	return []protocol.CompletionItem{
		{Label: "true", Kind: protocol.CompletionItemKindValue, Detail: "bool", InsertText: "true"},
		{Label: "false", Kind: protocol.CompletionItemKindValue, Detail: "bool", InsertText: "false"},
	}
}

// pathParamCompletions answers `/{|}` with the request fields that can bind a
// path segment and are not in the template yet; nil without a request clause.
// The clause is read from tokens: the parser takes an unfinished `{}` for the body.
func (s *Server) pathParamCompletions(view snapshotView, currentURI, currentSrc string, brace int) []protocol.CompletionItem {
	name := requestTypeAfter(view, brace)
	if name == "" {
		return nil
	}
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	td, ok := v.lookup(name, semantic.TypeDecls).(*ast.TypeDecl)
	if !ok {
		return nil
	}
	used := pathParamsBefore(view, brace)
	var out []protocol.CompletionItem
	for _, mem := range td.Body {
		f, ok := mem.(*ast.Field)
		if !ok || used[f.Name] || !pathBindableField(v, f) {
			continue
		}
		out = append(out, protocol.CompletionItem{
			Label:         f.Name,
			Kind:          protocol.CompletionItemKindField,
			Detail:        typeRefString(f.Type) + " - field of " + td.Name,
			Documentation: strings.Join(f.Doc, "\n"),
			InsertText:    f.Name,
		})
	}
	return out
}

// requestTypeAfter returns the type of the `request` clause after token i, or
// "" when the method has none; the scan stops at the next verb or declaration.
func requestTypeAfter(view snapshotView, i int) string {
	for j := i + 1; j < len(view.tokens); j++ {
		switch view.tokens[j].Kind {
		case lexer.KwRequest:
			if j+1 >= len(view.tokens) || view.tokens[j+1].Kind != lexer.Ident {
				return ""
			}
			return qualifiedNameAt(view, j+1)
		case lexer.VerbGet, lexer.VerbPost, lexer.VerbPut, lexer.VerbPatch,
			lexer.VerbDelete, lexer.VerbHead, lexer.VerbOptions,
			lexer.KwType, lexer.KwEnum, lexer.KwError, lexer.KwScalar,
			lexer.KwService, lexer.KwExtend, lexer.KwMiddleware, lexer.KwEvent:
			return ""
		}
	}
	return ""
}

// pathParamsBefore returns the parameters the template binds before brace i,
// on its line.
func pathParamsBefore(view snapshotView, i int) map[string]bool {
	used := map[string]bool{}
	line := view.tokens[i].Pos.Line
	for j := 0; j+2 < i; j++ {
		if view.tokens[j].Pos.Line != line || view.tokens[j].Kind != lexer.LBrace {
			continue
		}
		if view.tokens[j+2].Kind == lexer.RBrace {
			used[view.tokens[j+1].Text] = true
		}
	}
	return used
}

// pathBindableField reports whether f can bind a `{param}`: a required,
// non-array, non-map field with no other binding decorator, whose type is a
// wire-parseable built-in, an enum or a scalar over one.
func pathBindableField(v projectView, f *ast.Field) bool {
	t := f.Type
	if t == nil || t.Named == nil || t.Map != nil || t.Array || t.Optional {
		return false
	}
	for _, d := range []string{"query", "header", "cookie", "body", "form"} {
		if ast.HasDecorator(f.Decorators, d) {
			return false
		}
	}
	name := t.Named.Name.String()
	if prims.Is(name) {
		return prims.IsWireParseable(name)
	}
	switch d := v.lookup(name, semantic.EnumDecls|semantic.ScalarDecls).(type) {
	case *ast.EnumDecl:
		return true
	case *ast.ScalarDecl:
		return prims.IsWireParseable(d.Primitive)
	}
	return false
}

// serviceNameCompletions offers the primary services of the buffer's package,
// the names an `extend service` can target.
func (s *Server) serviceNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	v := s.loadProject(uriToPath(currentURI), currentSrc)
	pkg := v.proj.Packages[v.currentPackage()]
	if pkg == nil {
		return nil
	}
	return declItems(pkg, semantic.ServiceDecls, protocol.CompletionItemKindInterface, map[string]bool{})
}

// declItems offers the declarations of kinds in pkg whose name is not in seen,
// adding each name to seen.
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

// projectDeclItems offers the declarations of kinds across the project, one per
// name (the first package by name wins), sorted by label.
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

// declKind spells d's kind as the source does: `type`, `error NotFound`, ...
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

// kindDetail renders `kind (pkg)`, or kind alone for an unnamed package.
func kindDetail(kind, pkg string) string {
	if pkg == "" {
		return kind
	}
	return kind + " (" + pkg + ")"
}

// securitySchemeCompletions offers the manifest's openapi.securitySchemes, with
// each scheme's type and its scheme or location as detail; nil when there are none.
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

// middlewareNameCompletions offers every middleware in the project.
func (s *Server) middlewareNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	return s.projectDeclItems(currentURI, currentSrc, semantic.MiddlewareDecls, protocol.CompletionItemKindFunction)
}

// errorNameCompletions offers every error in the project.
func (s *Server) errorNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	return s.projectDeclItems(currentURI, currentSrc, semantic.ErrorDecls, protocol.CompletionItemKindClass)
}

// typeCompletionsProjectWide offers what a field type can name: the built-ins,
// `map`, and every type, enum and scalar in the project.
func (s *Server) typeCompletionsProjectWide(currentURI, currentSrc string) []protocol.CompletionItem {
	items := primitiveCompletions()
	items = append(items, keywordCompletions("map")...)
	items = append(items, s.declCompletions(currentURI, currentSrc, typePositionDecls)...)
	return items
}

// primitiveCompletions offers every documented built-in; `object` has no doc,
// being legal only inside an `@example({...})` literal.
func primitiveCompletions() []protocol.CompletionItem {
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
	return items
}

// scalarPrimitiveCompletions offers the built-ins `scalar Name |` accepts: those
// [semantic.PrimFromName] classifies, except file.
func scalarPrimitiveCompletions() []protocol.CompletionItem {
	var out []protocol.CompletionItem
	for _, sp := range prims.All() {
		p := semantic.PrimFromName(sp.Name)
		if p == 0 || p == semantic.PrimFile {
			continue
		}
		out = append(out, protocol.CompletionItem{
			Label:  sp.Name,
			Kind:   protocol.CompletionItemKindKeyword,
			Detail: "built-in",
		})
	}
	return out
}

// typePositionDecls is the set of declaration kinds a field's type may name.
const typePositionDecls = semantic.TypeDecls | semantic.EnumDecls | semantic.ScalarDecls

// clauseTypeCompletions offers the `type` declarations, the only kind a
// `request`, `response` or `payload` clause accepts.
func (s *Server) clauseTypeCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	return s.declCompletions(currentURI, currentSrc, semantic.TypeDecls)
}

// declCompletions offers every declaration of kinds in the project - bare in
// the buffer's package, `pkg.Name` elsewhere - plus each other package as `pkg.`.
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
	case *ast.EventDecl:
		return protocol.CompletionItemKindEvent
	case *ast.ServiceDecl:
		return protocol.CompletionItemKindInterface
	}
	return protocol.CompletionItemKindClass
}
