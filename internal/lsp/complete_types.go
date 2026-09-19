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
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// defaultValueCompletions answers `@default(<cursor>)` from the field the
// cursor sits on. `@default` takes ArgAny, so the legal set is whatever
// the FIELD's type accepts, and only two types have a closed one: an
// enum offers its values, a bool offers `true` / `false`. Every other
// type takes a free literal and gets nil, so the popup stays out of the
// way. A scalar is followed to the primitive it wraps, so
// `scalar Flag bool` behaves like `bool`.
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

// boolLiteralCompletions is the two-value set a bool-typed slot accepts.
func boolLiteralCompletions() []protocol.CompletionItem {
	return []protocol.CompletionItem{
		{Label: "true", Kind: protocol.CompletionItemKindValue, Detail: "bool", InsertText: "true"},
		{Label: "false", Kind: protocol.CompletionItemKindValue, Detail: "bool", InsertText: "false"},
	}
}

// pathParamCompletions answers `get Name /store/{<cursor>}` with the
// request type's field names. A `{param}` binds to the request field of
// the same name (the auto-@path rule), so the request type IS the closed
// set - no guessing involved. Fields that cannot source a path segment
// (optional, array, map, or a type with no single-value wire form) are
// left out, as are the parameters the template already uses.
//
// The clause is read from the TOKEN stream rather than the AST: an
// unfinished `{}` is not a path parameter to [parser.parsePath], which
// takes it for the method body and leaves the real body - and with it
// the request clause - unparsed. The tokens survive that.
//
// The route is authored before the body in plenty of sessions, and then
// there is no request clause to read: the answer is nil rather than a
// guess at what the type will be.
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

// requestTypeAfter returns the type named by the `request` clause that
// follows the token at i, or "" when the method declares none. The scan
// stops at the next verb or declaration keyword so a later method's
// clause is never borrowed.
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

// pathParamsBefore returns the parameter names the route template already
// binds ahead of the brace at i. A template is written on one line, so
// the line bounds the scan.
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

// pathBindableField reports whether f can source a `{param}` segment: a
// single, always-present value with a wire-string form, bound from the
// path rather than from somewhere else. Mirrors the analyser's
// auto-@path rule, which rejects an optional, array or struct-shaped
// field in that position, and only promotes a field that carries no
// binding decorator of its own.
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
	items := primitiveCompletions()
	// `map<K, V>` is a type, not a declaration keyword: a type position
	// is the only place it is legal. Its snippet lives with the other
	// keyword snippets rather than being spelled a second time here.
	items = append(items, keywordCompletions("map")...)
	items = append(items, s.declCompletions(currentURI, currentSrc, typePositionDecls)...)
	return items
}

// primitiveCompletions is one item per documented built-in. `object` is
// the only one left out: it carries no Doc because it is legal solely
// inside an `@example({...})` literal, never as a written type.
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

// scalarPrimitiveCompletions is the closed set a `scalar Name <cursor>`
// slot accepts: a built-in that lowers to a Go type. `any`, `object`
// and `file` are excluded - the analyser rejects all three there - and
// so is every declared type, including other scalars: a scalar wraps a
// built-in, never another declaration.
//
// [semantic.PrimFromName] is the same oracle the analyser's check reads,
// so the popup and the diagnostic cannot drift apart.
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

// typePositionDecls is every declaration kind a field's type may name.
// A middleware, a service and an event are declarations the analyser
// reports as an unknown TYPE when one appears in that slot, and an
// error is not referenceable either (`@errors(...)` has its own
// resolution path) - so the three type-shaped kinds are the whole set.
const typePositionDecls = semantic.TypeDecls | semantic.EnumDecls | semantic.ScalarDecls

// clauseTypeCompletions answers the `request` / `response` / `payload`
// slots, which name a message rather than any type. All three reject an
// enum and a scalar - neither has fields to bind or decode - so only
// `type` declarations are offered, and `map` is left out because the
// clause parses a named reference, not a type expression.
//
// `payload` is the strict one: the analyser demands a `type`
// declaration in so many words. A method clause additionally gets the
// built-ins, which it does not reject; the popup follows the analyser
// rather than second-guessing it.
func (s *Server) clauseTypeCompletions(currentURI, currentSrc string, kw lexer.Kind) []protocol.CompletionItem {
	items := s.declCompletions(currentURI, currentSrc, semantic.TypeDecls)
	if kw == lexer.KwPayload {
		return items
	}
	return append(primitiveCompletions(), items...)
}

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
	case *ast.EventDecl:
		return protocol.CompletionItemKindEvent
	case *ast.ServiceDecl:
		return protocol.CompletionItemKindInterface
	}
	return protocol.CompletionItemKindClass
}
