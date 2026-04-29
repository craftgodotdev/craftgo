package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/config"
	"github.com/dropship-dev/craftgo/internal/lexer"
	"github.com/dropship-dev/craftgo/internal/semantic"
)

// onCompletion answers `textDocument/completion`. The strategy is
// context-driven, with a project-wide fallback so the user always sees
// declared types alongside keywords:
//
//  1. Inside a decorator (`@…`) → decorators filtered by site level.
//  2. Inside an `import "…"` literal → sibling packages from the
//     design root.
//  3. After a qualified prefix `pkg.…` → decls inside that package.
//  4. After `request` / `response` / `:` / `<` / `,` (a type position)
//     → declared types (project-wide) + built-in primitives.
//  5. Anywhere else → keywords + project-wide types as a single
//     blended list. VSCode handles client-side filtering by prefix.
func (s *Server) onCompletion(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.CompletionParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, &protocol.CompletionList{}, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	items := s.completionsAt(view, params.Position, string(params.TextDocument.URI), src)
	return reply(ctx, &protocol.CompletionList{IsIncomplete: false, Items: items}, nil)
}

// completionsAt returns the candidate items for a cursor in view.
func (s *Server) completionsAt(view snapshotView, pos protocol.Position, currentURI, currentSrc string) []protocol.CompletionItem {
	prev, mid := surroundingTokens(view, pos)
	// Inside an import string literal (`import "…|"`) — suggest
	// available package paths.
	if isInsideImportString(view, pos) {
		prefix := importStringPrefix(view, pos)
		return s.importPathCompletions(currentURI, currentSrc, prefix)
	}
	// Inside a decorator argument list `@name(…|…)` — surface the
	// registered enum values when the spec restricts them. Must run
	// before the qualified-ref check because the cursor sits between
	// `(` and `)` so the surrounding-token analysis would otherwise
	// route to the type-position branch.
	if name, ok := decoratorArgContext(view, pos); ok {
		if items := decoratorArgCompletions(view, pos, name); items != nil {
			return items
		}
	}
	// Qualified ref `pkg.<cursor>` — list only the named package's
	// decls. Two cursor positions both qualify as "just after the
	// dot":
	//
	//   - Cursor on the dot itself (mid = Dot) — happens when the
	//     user has typed `shared.` and the next non-whitespace token
	//     starts on the next column. tokenAt's inclusive end-column
	//     check returns the dot.
	//   - Cursor on the identifier following the dot (mid = Ident,
	//     prev = Dot) — happens once the user starts typing the
	//     member name.
	//
	// Both must come BEFORE the decorator branch so an in-progress
	// `pkg.` shape is not mistaken for a decorator context.
	if mid != nil && mid.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, mid); ok {
			return s.packageDeclCompletions(view, currentURI, currentSrc, pkg)
		}
	}
	if prev != nil && prev.Kind == lexer.Dot {
		if pkg, ok := identBefore(view, prev); ok {
			return s.packageDeclCompletions(view, currentURI, currentSrc, pkg)
		}
	}
	// Decorator name completion — cursor on (or right after) `@`, or
	// inside an identifier whose preceding token is `@`.
	if mid != nil && mid.Kind == lexer.At {
		return decoratorCompletions(view, pos, "")
	}
	if mid != nil && mid.Kind == lexer.Ident && prev != nil && prev.Kind == lexer.At {
		return decoratorCompletions(view, pos, mid.Text)
	}
	// Type position: include builtins + every declared type
	// (project-wide).
	if prev != nil && isTypePositionTrigger(*prev) {
		return s.typeCompletionsProjectWide(view, currentURI, currentSrc)
	}
	// General context — keywords + project-wide declared types so
	// users typing identifiers see what they have already defined.
	items := keywordCompletions()
	items = append(items, s.declCompletionsProjectWide(view, currentURI, currentSrc)...)
	return items
}

// surroundingTokens returns the tokens immediately before and at the
// cursor. The "mid" token is the one whose span the cursor sits in
// (typically the identifier being typed); "prev" is the most recent
// non-trivia token before that.
func surroundingTokens(view snapshotView, pos protocol.Position) (prev, mid *lexer.Token) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx >= 0 {
		mid = &view.tokens[idx]
	}
	scan := idx
	if scan < 0 {
		scan = len(view.tokens) - 1
	}
	for i := scan - 1; i >= 0; i-- {
		t := view.tokens[i]
		if t.Kind == lexer.EOF {
			continue
		}
		prev = &view.tokens[i]
		break
	}
	return prev, mid
}

func isTypePositionTrigger(t lexer.Token) bool {
	switch t.Kind {
	case lexer.KwRequest, lexer.KwResponse, lexer.KwStream,
		lexer.Colon, lexer.LAngle, lexer.Comma:
		return true
	}
	return false
}

// isInsideImportString reports whether pos lies inside an `import "…"`
// string literal — the cursor sits between the two double-quotes that
// follow an `import` keyword. We rely on token-level inspection rather
// than re-lexing the partial line because the editor may send a cursor
// position that splits a token mid-string.
func isInsideImportString(view snapshotView, pos protocol.Position) bool {
	line := int(pos.Line) + 1
	col := int(pos.Character) + 1
	for i, t := range view.tokens {
		if t.Kind != lexer.KwImport {
			continue
		}
		// Look ahead for an optional alias ident, then a String token
		// on the same logical statement.
		for j := i + 1; j < len(view.tokens) && j < i+4; j++ {
			tk := view.tokens[j]
			if tk.Kind == lexer.String {
				start := tk.Pos
				end := tk.Pos
				end.Column += len(tk.Text)
				if start.Line == line && start.Column <= col && col <= end.Column {
					return true
				}
				break
			}
			if tk.Kind != lexer.Ident {
				break
			}
		}
	}
	return false
}

// identBefore returns the identifier token immediately before t (skipping
// only whitespace, which the tokenizer has already stripped). Returns ok
// = false when the previous token is not an identifier.
func identBefore(view snapshotView, t *lexer.Token) (string, bool) {
	idx := -1
	for i := range view.tokens {
		if &view.tokens[i] == t {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return "", false
	}
	prev := view.tokens[idx-1]
	if prev.Kind != lexer.Ident {
		return "", false
	}
	return prev.Text, true
}

// importPathCompletions walks the design root and returns one item per
// subdirectory that contains at least one `.craftgo` file. Labels are
// the directory path relative to the design root, matching the literal
// the user is expected to type inside `import "…"` (e.g. `shared`,
// `v1/api`, `auth/oauth`). The current file's own directory is
// filtered out so users do not import themselves.
func (s *Server) importPathCompletions(currentURI, currentSrc, prefix string) []protocol.CompletionItem {
	fsPath := uriToPath(currentURI)
	if fsPath == "" {
		return nil
	}
	_, _, designDir, err := config.Find(filepath.Dir(fsPath))
	if err != nil {
		return nil
	}
	currentDir, _ := filepath.Abs(filepath.Dir(fsPath))
	seen := map[string]struct{}{}
	var out []protocol.CompletionItem
	_ = filepath.WalkDir(designDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || !d.IsDir() {
			return nil
		}
		abs, _ := filepath.Abs(p)
		if abs == currentDir {
			return nil
		}
		// A directory only counts as an import target if it actually
		// contains a `.craftgo` source file. This filters out empty
		// nesting parents like `v1/` (when only `v1/api/foo.craftgo`
		// exists) so users see meaningful suggestions.
		entries, _ := os.ReadDir(p)
		hasCraftgo := false
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".craftgo" {
				hasCraftgo = true
				break
			}
		}
		if !hasCraftgo {
			return nil
		}
		rel, err := filepath.Rel(designDir, p)
		if err != nil || rel == "." {
			return nil
		}
		// Use forward slashes — the DSL stores import paths in POSIX
		// form regardless of host OS, matching the rest of the toolchain.
		rel = filepath.ToSlash(rel)
		if _, dup := seen[rel]; dup {
			return nil
		}
		// Filter by what the user has typed inside the quotes so far.
		// Without this, `import "shared/<cursor>"` would still see
		// `users`, `orders`, etc. as suggestions because VSCode's
		// fuzzy filter does not look past the leading `/`.
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			return nil
		}
		seen[rel] = struct{}{}
		out = append(out, protocol.CompletionItem{
			Label:  rel,
			Kind:   protocol.CompletionItemKindModule,
			Detail: "package",
		})
		return nil
	})
	return out
}

// decoratorArgContext detects whether pos sits inside a `@name(…)`
// argument list and returns the decorator's bare name when it does.
// The walk is purely token-based: we step backwards from the cursor,
// tracking parenthesis depth, until we land on an opening `(` whose
// preceding tokens spell `@Ident`. A `)` along the way pops the depth
// counter — once it goes negative we have left every enclosing
// decorator and the cursor is not in an arg list.
func decoratorArgContext(view snapshotView, pos protocol.Position) (string, bool) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	depth := 0
	for i := idx - 1; i >= 0; i-- {
		t := view.tokens[i]
		switch t.Kind {
		case lexer.RParen:
			depth++
		case lexer.LParen:
			if depth > 0 {
				depth--
				continue
			}
			// Found the unmatched `(`. Look two tokens back for
			// `@<ident>`. The Ident may be either a plain identifier
			// or one of the keyword-spelt decorators (`@stream`).
			if i >= 2 && view.tokens[i-2].Kind == lexer.At {
				return view.tokens[i-1].Text, true
			}
			return "", false
		}
	}
	return "", false
}

// decoratorArgCompletions returns enum-value completions for the
// decorator at the cursor when the spec restricts them. The level
// is inferred from the surrounding declaration so `@format` on a
// method emits streaming wire formats while `@format` on a field
// emits string formats. Returns nil to signal "no enum applies — let
// the next branch handle this position".
func decoratorArgCompletions(view snapshotView, pos protocol.Position, name string) []protocol.CompletionItem {
	spec, ok := semantic.Registry[name]
	if !ok {
		return nil
	}
	values := spec.Args.Enum
	if guessLevel(view, pos) == semantic.LvlMethod && len(spec.MethodEnum) > 0 {
		values = spec.MethodEnum
	}
	if len(values) == 0 {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(values))
	for _, v := range values {
		out = append(out, protocol.CompletionItem{
			Label:      v,
			Kind:       protocol.CompletionItemKindEnumMember,
			Detail:     "@" + name + " value",
			InsertText: v,
		})
	}
	return out
}

// importStringPrefix returns the substring of the `import "…"` literal
// that lies between the opening quote and the cursor — used as the
// prefix filter for [importPathCompletions]. Returns an empty string
// when the cursor is at the very start of the literal.
func importStringPrefix(view snapshotView, pos protocol.Position) string {
	line := int(pos.Line) + 1
	col := int(pos.Character) + 1
	for i, t := range view.tokens {
		if t.Kind != lexer.KwImport {
			continue
		}
		for j := i + 1; j < len(view.tokens) && j < i+4; j++ {
			tk := view.tokens[j]
			if tk.Kind == lexer.String {
				start := tk.Pos
				if start.Line != line {
					return ""
				}
				// Token text includes both surrounding quotes — skip
				// the first.
				typed := tk.Text
				if len(typed) > 0 && typed[0] == '"' {
					typed = typed[1:]
				}
				// How many runes between the opening quote and the
				// cursor? Column-based math is OK because the lexer
				// uses 1-indexed runes.
				offset := col - (start.Column + 1)
				if offset <= 0 {
					return ""
				}
				if offset > len(typed) {
					offset = len(typed)
				}
				return typed[:offset]
			}
			if tk.Kind != lexer.Ident {
				break
			}
		}
	}
	return ""
}

// packageDeclCompletions returns every top-level declaration in the
// named sibling package, suitable for offering completion on the right
// side of a qualified reference (`shared.<cursor>` or `x.<cursor>`
// where `x` is an import alias).
func (s *Server) packageDeclCompletions(view snapshotView, currentURI, currentSrc, pkg string) []protocol.CompletionItem {
	files, root := s.projectFilesWithRoot(uriToPath(currentURI), currentSrc)
	imports := currentImports(view.file)

	// Resolve `pkg` via imports first — it might be an alias rather
	// than a literal package name. Falls back to a Package.Name match.
	targetDir := ""
	for _, imp := range imports {
		if imp == nil {
			continue
		}
		if imp.Alias == pkg {
			targetDir = importTargetDir(root, imp.Path)
			break
		}
		if imp.Alias == "" && lastPathSegment(imp.Path) == pkg {
			targetDir = importTargetDir(root, imp.Path)
			break
		}
	}

	var out []protocol.CompletionItem
	for _, p := range files {
		if p.file == nil {
			continue
		}
		matchByDir := targetDir != "" && inDir(p.path, targetDir)
		matchByName := p.file.Package != nil && p.file.Package.Name == pkg
		if !matchByDir && !matchByName {
			continue
		}
		for _, d := range p.file.Decls {
			out = append(out, protocol.CompletionItem{
				Label:         d.DeclName(),
				Kind:          declSymbolKindToCompletion(d),
				Detail:        declSummary(d),
				Documentation: strings.Join(declDoc(d), "\n"),
			})
		}
	}
	return out
}

// typeCompletionsProjectWide is the type-position equivalent of
// [typeCompletions] but scoped to the entire project rather than just
// the current file. Built-in primitives are listed alongside.
func (s *Server) typeCompletionsProjectWide(view snapshotView, currentURI, currentSrc string) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for name := range builtinDocs {
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   protocol.CompletionItemKindKeyword,
			Detail: "built-in",
		})
	}
	items = append(items, s.declCompletionsProjectWide(view, currentURI, currentSrc)...)
	return items
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
// alias (e.g. `s` for `shared`) surfaces the package itself — picking
// it lets the user continue with `.SomeType` and reach the qualified
// completion path.
func (s *Server) declCompletionsProjectWide(view snapshotView, currentURI, currentSrc string) []protocol.CompletionItem {
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
			label := d.DeclName()
			insert := label
			detail := declSummary(d)
			if pkgName != "" && pkgName != currentPkg {
				label = pkgName + "." + d.DeclName()
				insert = label
				detail = pkgName + " — " + detail
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
// trailing path segment becomes the implicit alias — matching the
// resolution in [findDeclAcross]. Duplicate aliases are de-duped.
func importAliasesOf(f *ast.File) []string {
	if f == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, imp := range f.Imports {
		if imp == nil {
			continue
		}
		alias := imp.Alias
		if alias == "" {
			base := imp.Path
			for j := len(base) - 1; j >= 0; j-- {
				if base[j] == '/' {
					base = base[j+1:]
					break
				}
			}
			alias = base
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		out = append(out, alias)
	}
	return out
}

// localDeclItems is the fall-back used when no project root can be
// found — callers reach here for stand-alone files outside a craftgo
// design layout.
func localDeclItems(view snapshotView) []protocol.CompletionItem {
	if view.file == nil {
		return nil
	}
	out := make([]protocol.CompletionItem, 0, len(view.file.Decls))
	for _, d := range view.file.Decls {
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
// `prefix` lets the editor narrow as the user types — in practice the
// LSP client also filters, so an empty prefix is fine.
func decoratorCompletions(view snapshotView, pos protocol.Position, prefix string) []protocol.CompletionItem {
	level := guessLevel(view, pos)
	names := make([]string, 0, len(semantic.Registry))
	for name := range semantic.Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		spec := semantic.Registry[name]
		if level != 0 && spec.Levels != 0 && spec.Levels&level == 0 {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		insert := name
		if needsArgs(spec.Args) {
			insert = name + "($0)"
		}
		out = append(out, protocol.CompletionItem{
			Label:            name,
			Kind:             protocol.CompletionItemKindFunction,
			Detail:           argsRuleSummary(spec.Args),
			Documentation:    spec.Doc,
			InsertText:       insert,
			InsertTextFormat: protocol.InsertTextFormatSnippet,
		})
	}
	return out
}

func needsArgs(r semantic.ArgsRule) bool {
	return r.Min > 0 || r.Variadic != 0 || r.Max > 0
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

// keywordCompletions lists the always-relevant top-level keywords.
// Verbs intentionally appear too — they are valid identifiers when
// declaring methods inside a service body, and the LSP client will
// filter out anything the user has not started typing.
func keywordCompletions() []protocol.CompletionItem {
	kw := []string{
		"package", "import", "type", "enum", "error", "scalar",
		"service", "extend", "middleware", "request", "response",
		"stream", "map", "true", "false", "null",
		"get", "post", "put", "patch", "delete", "head", "options",
	}
	out := make([]protocol.CompletionItem, 0, len(kw))
	for _, k := range kw {
		out = append(out, protocol.CompletionItem{
			Label: k,
			Kind:  protocol.CompletionItemKindKeyword,
		})
	}
	return out
}

// guessLevel inspects the AST around the cursor and returns the
// declaration-site mask that any decorator at the current position
// must satisfy. The mapping is conservative — the worst case is a
// zero return, which means "do not filter".
func guessLevel(view snapshotView, pos protocol.Position) semantic.Level {
	if view.file == nil {
		return 0
	}
	line := int(pos.Line) + 1
	// Find the most specific enclosing declaration by source line.
	var best ast.Decl
	for _, d := range view.file.Decls {
		if d.DeclPos().Line <= line {
			best = d
		}
	}
	if best == nil {
		return semantic.LvlFile
	}
	switch v := best.(type) {
	case *ast.TypeDecl:
		// Inside the body? Pick field-level; otherwise the type itself.
		if line > v.Pos.Line {
			return semantic.LvlField
		}
		return semantic.LvlType
	case *ast.EnumDecl:
		if line > v.Pos.Line {
			return semantic.LvlEnumValue
		}
		return semantic.LvlEnum
	case *ast.ErrorDecl:
		if v.HasBody && line > v.Pos.Line {
			return semantic.LvlField
		}
		return semantic.LvlError
	case *ast.ScalarDecl:
		return semantic.LvlScalar
	case *ast.MiddlewareDecl:
		return semantic.LvlMiddleware
	case *ast.ServiceDecl:
		// Inside a method body or on the method line itself?
		for _, m := range v.Methods {
			if m.Pos.Line == line {
				return semantic.LvlMethod
			}
		}
		if line > v.Pos.Line {
			return semantic.LvlMethod
		}
		return semantic.LvlService
	}
	return 0
}

