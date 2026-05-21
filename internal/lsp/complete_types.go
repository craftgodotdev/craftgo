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
	"github.com/craftgodotdev/craftgo/internal/parser"
)

func (s *Server) defaultEnumCompletions(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	f := fieldAtCursor(view, pos)
	if f == nil || f.Type == nil || f.Type.Named == nil || f.Type.Named.Name == nil {
		return nil
	}
	parts := f.Type.Named.Name.Parts
	if len(parts) != 1 {
		return nil
	}
	e := s.enumDeclByNameProjectWide(view, currentURI, currentSrc, parts[0])
	if e == nil {
		return nil
	}
	enumVals := e.EnumValues()
	out := make([]protocol.CompletionItem, 0, len(enumVals))
	for _, v := range enumVals {
		out = append(out, protocol.CompletionItem{
			Label:      v.Name,
			Kind:       protocol.CompletionItemKindEnumMember,
			Detail:     "value of enum " + e.Name,
			InsertText: v.Name,
		})
	}
	return out
}

// enumDeclByNameProjectWide walks every sibling `*.craftgo` file in
// the current project and returns the first matching enum decl.
// Multi-file packages declare enums anywhere - this lookup mirrors
// the way semantic resolves cross-file refs. Falls back to the
// current view's parsed file when the project walker yields nothing
// (typical in unit tests that parse a single in-memory snapshot
// without a backing filesystem entry).
func (s *Server) enumDeclByNameProjectWide(view snapshotView, currentURI, currentSrc, name string) *ast.EnumDecl {
	files, _ := s.projectFilesWithRoot(uriToPath(currentURI), currentSrc)
	for _, p := range files {
		if p.file == nil {
			continue
		}
		for _, d := range p.file.Decls {
			if e, ok := d.(*ast.EnumDecl); ok && e.Name == name {
				return e
			}
		}
	}
	if view.file != nil {
		for _, d := range view.file.Decls {
			if e, ok := d.(*ast.EnumDecl); ok && e.Name == name {
				return e
			}
		}
	}
	return nil
}

// durationSuffixes / sizeSuffixes mirror the unit set the lexer
// recognises in [lexer.lexNumber]; keep these in sync if the lexer
// gains new units.

func (s *Server) serviceNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	files := s.projectASTs(uriToPath(currentURI), currentSrc)
	currentPkg := ""
	currentPath := uriToPath(currentURI)
	for _, p := range files {
		if p.path == currentPath && p.file != nil && p.file.Package != nil {
			currentPkg = p.file.Package.Name
			break
		}
	}
	seen := map[string]struct{}{}
	var out []protocol.CompletionItem
	for _, p := range files {
		if p.file == nil || p.file.Package == nil {
			continue
		}
		if currentPkg != "" && p.file.Package.Name != currentPkg {
			continue
		}
		for _, d := range p.file.Decls {
			sd, ok := d.(*ast.ServiceDecl)
			if !ok || sd.Extend {
				continue
			}
			if _, dup := seen[sd.Name]; dup {
				continue
			}
			seen[sd.Name] = struct{}{}
			out = append(out, protocol.CompletionItem{
				Label:         sd.Name,
				Kind:          protocol.CompletionItemKindInterface,
				Detail:        "service (" + p.file.Package.Name + ")",
				Documentation: strings.Join(sd.Doc, "\n"),
				InsertText:    sd.Name,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
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

// middlewareNameCompletions enumerates every `middleware Name`
// declaration across the project so an `@middlewares(...)` argument
// list shows the same closed set the semantic resolver accepts.
// Names are emitted as Function-kind items because that is how
// editors render them with the closest icon to "function pointer
// the runtime calls" - the closest analogue available in LSP's
// CompletionItemKind set.
func (s *Server) middlewareNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	files := s.projectASTs(uriToPath(currentURI), currentSrc)
	seen := map[string]struct{}{}
	var out []protocol.CompletionItem
	for _, p := range files {
		if p.file == nil {
			continue
		}
		for _, d := range p.file.Decls {
			md, ok := d.(*ast.MiddlewareDecl)
			if !ok {
				continue
			}
			if _, dup := seen[md.Name]; dup {
				continue
			}
			seen[md.Name] = struct{}{}
			pkgName := ""
			if p.file.Package != nil {
				pkgName = p.file.Package.Name
			}
			detail := "middleware"
			if pkgName != "" {
				detail = "middleware (" + pkgName + ")"
			}
			out = append(out, protocol.CompletionItem{
				Label:         md.Name,
				Kind:          protocol.CompletionItemKindFunction,
				Detail:        detail,
				Documentation: strings.Join(md.Doc, "\n"),
				InsertText:    md.Name,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// errorNameCompletions enumerates every `error <Category> Name`
// declaration in the project so an `@errors(...)` decorator argument
// list shows the closed set of declared error types. Each item is
// emitted as Class-kind because the user-facing wire shape is a
// struct/class (matching how the generated Go code surfaces it as
// `<Name>Err`); editors render it with the same icon they use for
// any declared type, which keeps the visual grammar consistent
// across decorator args.
func (s *Server) errorNameCompletions(currentURI, currentSrc string) []protocol.CompletionItem {
	files := s.projectASTs(uriToPath(currentURI), currentSrc)
	if len(files) == 0 {
		// No `craftgo.design.yaml` upward from the buffer (running
		// outside a project root, common for first-touch editing).
		// Fall back to the current buffer so an unsaved file still
		// surfaces its own error decls.
		f := parser.New(uriToPath(currentURI), currentSrc).Parse()
		if f != nil {
			files = []projectAST{{path: uriToPath(currentURI), file: f}}
		}
	}
	seen := map[string]struct{}{}
	var out []protocol.CompletionItem
	for _, p := range files {
		if p.file == nil {
			continue
		}
		for _, d := range p.file.Decls {
			ed, ok := d.(*ast.ErrorDecl)
			if !ok {
				continue
			}
			if _, dup := seen[ed.Name]; dup {
				continue
			}
			seen[ed.Name] = struct{}{}
			pkgName := ""
			if p.file.Package != nil {
				pkgName = p.file.Package.Name
			}
			detail := "error " + ed.Category
			if pkgName != "" {
				detail = "error " + ed.Category + " (" + pkgName + ")"
			}
			out = append(out, protocol.CompletionItem{
				Label:         ed.Name,
				Kind:          protocol.CompletionItemKindClass,
				Detail:        detail,
				Documentation: strings.Join(ed.Doc, "\n"),
				InsertText:    ed.Name,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// importStringPrefix returns the substring of the `import "…"` literal
// that lies between the opening quote and the cursor - used as the
// prefix filter for [importPathCompletions]. Returns an empty string
// when the cursor is at the very start of the literal.

func (s *Server) typeCompletionsProjectWide(view snapshotView, currentURI, currentSrc string) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for name := range builtinDocs {
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   protocol.CompletionItemKindKeyword,
			Detail: "built-in",
		})
	}
	items = append(items, s.declCompletionsFiltered(view, currentURI, currentSrc, declCompletionTypePosition)...)
	return items
}

// declCompletionFilter selects which top-level decl kinds appear in a
// completion list. Today only [declCompletionTypePosition] exists -
// the indirection stays for future contexts (e.g. error-position
// inside `@errors(...)`) that need a different filter without
// duplicating the project-walk loop.
type declCompletionFilter func(ast.Decl) bool

// declCompletionTypePosition drops error declarations so type-position
// completions only suggest decls that actually resolve as types.
// Used as the default for [declCompletionsProjectWide] because errors
// are never referenceable as a standalone symbol - `@errors(...)`
// has its own resolution path.
func declCompletionTypePosition(d ast.Decl) bool {
	_, isError := d.(*ast.ErrorDecl)
	return !isError
}

// declCompletionsProjectWide gathers every top-level declaration across
// the project and exposes them as completion items. Cross-package decls
// are surfaced with the qualified `pkg.Name` form as both the label
// AND insertText so picking the item lands a full reference at the
// cursor (otherwise the user would land just `Name` and would still
// have to type `pkg.` themselves). Same-package decls keep their bare
// label because qualifying is illegal in self-references.
//
// In addition to declarations, every imported package alias is
// emitted as a Module-kind item so that typing the first letter of an
// alias (e.g. `s` for `shared`) surfaces the package itself - picking
// it lets the user continue with `.SomeType` and reach the qualified
// completion path.
// declCompletionsProjectWide is the default project-wide decl
// suggester. Errors are filtered out unconditionally - they are not
// usable as standalone references in any user-facing position
// (`@errors(...)` has its own resolver, and field-type / request /
// response usage is rejected by the semantic phase). Surfacing them
// would mislead the user into a guaranteed-to-fail picker.
func (s *Server) declCompletionsProjectWide(view snapshotView, currentURI, currentSrc string) []protocol.CompletionItem {
	return s.declCompletionsFiltered(view, currentURI, currentSrc, declCompletionTypePosition)
}

// declCompletionsFiltered is the workhorse behind the project-wide
// declaration completions. The filter callback decides which decls
// reach the result list - type-position contexts pass
// [declCompletionTypePosition] to drop errors; everywhere else
// passes [declCompletionAll] to keep the legacy behaviour. Import
// aliases are emitted unconditionally - they are not declarations
// and the user might want them in any completion context.
func (s *Server) declCompletionsFiltered(view snapshotView, currentURI, currentSrc string, keep declCompletionFilter) []protocol.CompletionItem {
	files := s.projectASTs(uriToPath(currentURI), currentSrc)
	if len(files) == 0 {
		return localDeclItems(view)
	}
	currentPkg := ""
	if view.file != nil && view.file.Package != nil {
		currentPkg = view.file.Package.Name
	}
	var items []protocol.CompletionItem
	for _, p := range files {
		if p.file == nil {
			continue
		}
		pkgName := ""
		if p.file.Package != nil {
			pkgName = p.file.Package.Name
		}
		for _, d := range p.file.Decls {
			if !keep(d) {
				continue
			}
			label := d.DeclName()
			insert := label
			detail := declSummary(d)
			if pkgName != "" && pkgName != currentPkg {
				label = pkgName + "." + d.DeclName()
				insert = label
				detail = pkgName + " - " + detail
			}
			items = append(items, protocol.CompletionItem{
				Label:         label,
				Kind:          declSymbolKindToCompletion(d),
				Detail:        detail,
				Documentation: strings.Join(declDoc(d), "\n"),
				InsertText:    insert,
			})
		}
	}
	for _, alias := range importAliasesOf(view.file) {
		items = append(items, protocol.CompletionItem{
			Label:      alias,
			Kind:       protocol.CompletionItemKindModule,
			Detail:     "imported package",
			InsertText: alias + ".",
		})
	}
	return items
}

// importAliasesOf returns every alias the file's imports expose at
// the type-position level. Explicit aliases win; otherwise the
// trailing path segment becomes the implicit alias - matching the
// resolution in [findDeclAcross]. Duplicate aliases are de-duped.

func localDeclItems(view snapshotView) []protocol.CompletionItem {
	if view.file == nil {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(view.file.Decls))
	for _, d := range view.file.Decls {
		if _, isError := d.(*ast.ErrorDecl); isError {
			continue
		}
		out = append(out, protocol.CompletionItem{
			Label:         d.DeclName(),
			Kind:          declSymbolKindToCompletion(d),
			Detail:        declSummary(d),
			Documentation: strings.Join(declDoc(d), "\n"),
		})
	}
	return out
}

// decoratorCompletions enumerates the registry, optionally filtered by
// a declaration-level guess inferred from the cursor's surroundings.
// `prefix` lets the editor narrow as the user types - in practice the
// LSP client also filters, so an empty prefix is fine.

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
