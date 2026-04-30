package lsp

import (
	"context"
	"encoding/json"
	"fmt"
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
	// After `extend service ` — list every primary service name in
	// the project so the user can pick which one this block extends.
	if isExtendServiceContext(view, pos) {
		return s.serviceNameCompletions(currentURI, currentSrc)
	}
	// Inside a decorator argument list `@name(…|…)` — surface the
	// registered enum values when the spec restricts them, OR the
	// project's declared middleware names for `@middlewares(...)`.
	// Must run before the qualified-ref check because the cursor sits
	// between `(` and `)` so the surrounding-token analysis would
	// otherwise route to the type-position branch.
	if name, ok := decoratorArgContext(view, pos); ok {
		if name == "middlewares" {
			return s.middlewareNameCompletions(currentURI, currentSrc)
		}
		// `@security(<scheme>, scopes: [...])` — arg 1 is a scheme
		// identifier that must resolve to a key in the project's
		// `openapi.securitySchemes` map. Suggest the declared keys
		// only at the arg-1 slot:
		//
		//   - cursor right after `(` (mid = LParen, no content yet),
		//   - or mid-typing the scheme ident (mid = Ident,
		//     prev = LParen).
		//
		// Anywhere past the first comma we are inside `scopes: [...]`
		// (or further), where the items are application-defined
		// strings, not scheme names — fall through to the generic
		// branch instead.
		atArgOne := false
		if mid != nil && mid.Kind == lexer.LParen {
			atArgOne = true
		} else if prev != nil && prev.Kind == lexer.LParen {
			atArgOne = true
		}
		if name == "security" && atArgOne {
			if items := s.securitySchemeCompletions(currentURI); items != nil {
				return items
			}
		}
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
	// `error <Category>` position — fires when the cursor sits right
	// after the `error` keyword (mid is nil or the in-progress
	// category identifier). The 19 reserved HTTP categories are a
	// closed set, so completion is the obvious affordance.
	if prev != nil && prev.Kind == lexer.KwError && (mid == nil || mid.Kind == lexer.Ident) {
		return errorCategoryCompletions()
	}
	// Just opened a block — cursor right after `{` with no
	// in-progress identifier. Auto-suggest here is purely noise: the
	// user has not signalled what they're about to type, and the
	// project-wide-decls dump shadows whatever they actually wanted.
	// Return empty so VS Code's popup stays out of the way; users who
	// invoke completion manually (or start typing a letter) will land
	// in the regular branches below.
	//
	// `mid` may be the matching `}` (when the cursor sits inside an
	// empty `{}`), nil (cursor on whitespace), or absent. Anything
	// other than an in-progress identifier counts as "no signal yet".
	if prev != nil && prev.Kind == lexer.LBrace && (mid == nil || mid.Kind != lexer.Ident) {
		return nil
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
// non-trivia token whose span ends at or before the cursor.
//
// The position-aware backward scan is important: when the cursor sits
// on whitespace the lexer has no token there, but the LAST token in
// the file may be AFTER the cursor (e.g. cursor on the blank line
// between `{` and `}` of a multi-line block). Falling back to
// "last token in the slice" would mis-name `prev` as the trailing
// `}` and break every completion branch that keys off `prev.Kind`.
func surroundingTokens(view snapshotView, pos protocol.Position) (prev, mid *lexer.Token) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx >= 0 {
		mid = &view.tokens[idx]
	}
	target := lexer.Position{Line: int(pos.Line) + 1, Column: int(pos.Character) + 1}
	scanFrom := idx - 1
	if idx < 0 {
		scanFrom = -1
		for i := len(view.tokens) - 1; i >= 0; i-- {
			t := view.tokens[i]
			if t.Kind == lexer.EOF {
				continue
			}
			end := t.Pos
			end.Column += len(t.Text)
			if posLessEq(end, target) {
				scanFrom = i
				break
			}
		}
	}
	for i := scanFrom; i >= 0; i-- {
		t := view.tokens[i]
		if t.Kind == lexer.EOF {
			continue
		}
		prev = &view.tokens[i]
		break
	}
	return prev, mid
}

// posLessEq reports whether a comes at or before b in source order.
// Lines win the comparison; columns tie-break within the same line.
func posLessEq(a, b lexer.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column <= b.Column
}

func isTypePositionTrigger(t lexer.Token) bool {
	switch t.Kind {
	case lexer.KwRequest, lexer.KwResponse,
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
//
// Walks include the cursor's own token (`idx`, not `idx-1`) so a
// cursor sitting exactly on the opening `(` — common right after the
// user types `@middlewares(` — still resolves cleanly. RParens are
// only counted when they're STRICTLY before the cursor; that keeps
// the closing paren of the decorator we're inside from prematurely
// flipping `depth` negative.
func decoratorArgContext(view snapshotView, pos protocol.Position) (string, bool) {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	depth := 0
	for i := idx; i >= 0; i-- {
		if i >= len(view.tokens) {
			continue
		}
		t := view.tokens[i]
		switch t.Kind {
		case lexer.RParen:
			// Skip the cursor's own RParen — we're INSIDE its
			// decorator, not after it.
			if i == idx {
				continue
			}
			depth++
		case lexer.LParen:
			if depth > 0 {
				depth--
				continue
			}
			// Found the unmatched `(`. Look two tokens back for
			// `@<ident>`. The Ident may be either a plain identifier
			// or one of the keyword-spelt decorators (`@true`).
			if i >= 2 && view.tokens[i-2].Kind == lexer.At {
				return view.tokens[i-1].Text, true
			}
			return "", false
		}
	}
	return "", false
}

// decoratorArgCompletions returns enum-value completions for the
// decorator at the cursor when the spec restricts them. Returns nil
// to signal "no enum applies — let the next branch handle this
// position".
func decoratorArgCompletions(view snapshotView, pos protocol.Position, name string) []protocol.CompletionItem {
	spec, ok := semantic.Registry[name]
	if !ok {
		return nil
	}
	_ = view
	_ = pos
	values := spec.Args.Enum
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

// isExtendServiceContext reports whether the cursor sits at the
// identifier slot of an `extend service <cursor>` clause. The check
// walks tokens backwards: if the two most recent non-cursor tokens
// (skipping any partial ident the user is typing) are `service` then
// `extend`, we are at the slot.
//
// Boundary handling: when the cursor sits past the last real token
// (tokenAt returned -1 because EOF is the only thing left),
// `idx == len(view.tokens)` and we must NOT index into the slice.
// Likewise the partial-ident skip needs to verify `idx` is in range
// before reading `view.tokens[idx]`.
func isExtendServiceContext(view snapshotView, pos protocol.Position) bool {
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if idx < 0 {
		idx = len(view.tokens)
	}
	// Skip a partial ident at the cursor — the user is mid-typing
	// the service name and we still want to fire.
	if idx >= 0 && idx < len(view.tokens) && view.tokens[idx].Kind == lexer.Ident {
		idx--
	}
	if idx < 2 {
		return false
	}
	prev := view.tokens[idx-1]
	prev2 := view.tokens[idx-2]
	return prev.Kind == lexer.KwService && prev2.Kind == lexer.KwExtend
}

// serviceNameCompletions enumerates primary `service Name`
// declarations that are valid extension targets from the cursor's
// current file. Extends resolve per-package, so cross-package
// services would always trip `service/extend-orphan` — including
// them in the completion list would mislead the user. The function
// therefore filters by the current file's package name.
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
// and the completion popup falls through to the generic branch —
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
			Value: fmt.Sprintf("**`%s`** — %s security scheme.\n\nDeclared in `craftgo.design.yaml` under `openapi.securitySchemes.%s`.", name, scheme.Type, name),
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
// the runtime calls" — the closest analogue available in LSP's
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
	// At field level, narrow further by the field's primitive type
	// so a `total int? @<cursor>` does not surface string-only or
	// array-only validators. Returns 0 (PrimAny) when the cursor is
	// not on a field row, in which case the AppliesTo filter is a
	// no-op and only the level filter applies.
	var fieldPrim semantic.Prims
	if level == semantic.LvlField {
		fieldPrim = fieldPrimAt(view, pos)
	}
	names := make([]string, 0, len(semantic.Registry))
	for name := range semantic.Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]protocol.CompletionItem, 0, len(names))
	for _, name := range names {
		spec := semantic.Registry[name]
		// Strict level filter: only surface decorators whose
		// declared site mask intersects the cursor's level. The
		// guard against `spec.Levels == 0` is defensive for any
		// future Registry entry without a Levels declaration —
		// treating "no levels" as "not applicable here" keeps the
		// completion list focused on supported decorators.
		if spec.Levels == 0 || spec.Levels&level == 0 {
			continue
		}
		// Per-primitive filter: at field level, drop validators
		// whose AppliesTo doesn't intersect the field's resolved
		// primitive. Decorators with AppliesTo == 0 (PrimAny) pass
		// through — they apply regardless of type.
		if fieldPrim != 0 && spec.AppliesTo != 0 && spec.AppliesTo&fieldPrim == 0 {
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

// errorCategoryStatus pairs each reserved HTTP error category with the
// status code emitted by the codegen — exposed on the completion item's
// Detail line so users see which HTTP code the category resolves to
// without leaving the editor. Mirrors the readonly table in
// internal/codegen/errors.go::categoryStatus; keep the two in sync.
var errorCategoryStatus = []struct {
	name   string
	status int
}{
	{"BadRequest", 400},
	{"Unauthorized", 401},
	{"PaymentRequired", 402},
	{"Forbidden", 403},
	{"NotFound", 404},
	{"MethodNotAllowed", 405},
	{"NotAcceptable", 406},
	{"Conflict", 409},
	{"Gone", 410},
	{"PreconditionFailed", 412},
	{"PayloadTooLarge", 413},
	{"UnsupportedMediaType", 415},
	{"UnprocessableEntity", 422},
	{"TooManyRequests", 429},
	{"Internal", 500},
	{"NotImplemented", 501},
	{"BadGateway", 502},
	{"ServiceUnavailable", 503},
	{"GatewayTimeout", 504},
}

// errorCategoryCompletions returns one completion item per reserved
// HTTP error category. Fired when the cursor sits in the
// `error <cursor>` position. Each item carries the HTTP status as
// Detail and a short doc snippet that the LSP client can render in
// the autocomplete popup.
func errorCategoryCompletions() []protocol.CompletionItem {
	out := make([]protocol.CompletionItem, 0, len(errorCategoryStatus))
	for _, c := range errorCategoryStatus {
		detail := fmt.Sprintf("HTTP %d", c.status)
		doc := protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: fmt.Sprintf("**`%s`** — built-in error category (HTTP %d).\n\nUse as `error %s YourErrorName` to declare an error of this kind.", c.name, c.status, c.name),
		}
		out = append(out, protocol.CompletionItem{
			Label:         c.name,
			Kind:          protocol.CompletionItemKindEnumMember,
			Detail:        detail,
			Documentation: doc,
		})
	}
	return out
}

// keywordCompletions lists the always-relevant top-level keywords.
// Verbs intentionally appear too — they are valid identifiers when
// declaring methods inside a service body, and the LSP client will
// filter out anything the user has not started typing.
func keywordCompletions() []protocol.CompletionItem {
	kw := []string{
		"package", "import", "type", "enum", "error", "scalar",
		"service", "extend", "middleware", "request", "response",
		"map", "true", "false", "null",
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

// guessLevel returns the decorator-site mask for the cursor's
// position. Three structural zones map to distinct levels:
//
//  1. Inside a decl body (between `{` and `}`) → field / method /
//     enum value, depending on the enclosing decl kind.
//  2. ABOVE a decl (the decorator zone — every `@…` line that
//     precedes a `type` / `service` / `enum` / `error` / `scalar` /
//     `middleware` keyword) → the level of THAT decl. This is what
//     the user expects when they hit `@` on a blank line above
//     `service Foo`: the completion list should surface
//     service-only decorators, not file-level ones.
//  3. Anywhere else (top of file before the first decl, between two
//     completed decls, etc.) → file level.
//
// The brace-depth scan disambiguates "inside prev's body" from
// "between prev and next" without needing end-position metadata on
// each AST node.
func guessLevel(view snapshotView, pos protocol.Position) semantic.Level {
	if view.file == nil {
		return semantic.LvlFile
	}
	line := int(pos.Line) + 1
	// File-header decorator zone: cursor sits AT or above the
	// `package` line — anything legal at file scope (`@title`,
	// `@version`, `@doc`) wins. Without this branch the zone above
	// `package` would be classified by the first decl below it,
	// which is almost always wrong (you can't put `@required`
	// before `package`).
	if view.file.Package != nil && line <= view.file.Package.Pos.Line {
		return semantic.LvlFile
	}
	var prevDecl, nextDecl ast.Decl
	for _, d := range view.file.Decls {
		if d.DeclPos().Line >= line {
			if nextDecl == nil {
				nextDecl = d
			}
		} else {
			prevDecl = d
		}
	}
	if prevDecl != nil && cursorInsideDeclBody(view, pos, prevDecl) {
		// Inside prev's body → field / method / enum value scope.
		// ErrorDecl without a body slot, ScalarDecl, and
		// MiddlewareDecl have no body to be inside; the brace-
		// depth check should already reject those, but we keep
		// the switch exhaustive so any future decl kind that
		// adds a body lands in the right bucket.
		switch v := prevDecl.(type) {
		case *ast.TypeDecl:
			return semantic.LvlField
		case *ast.EnumDecl:
			return semantic.LvlEnumValue
		case *ast.ErrorDecl:
			if v.HasBody {
				return semantic.LvlErrorField
			}
		case *ast.ServiceDecl:
			return semantic.LvlMethod
		}
	}
	if nextDecl != nil {
		// Decorator zone — the cursor is in a blank stretch ABOVE
		// nextDecl, where every `@…` line ends up as a decorator
		// for that decl.
		return declSiteLevel(nextDecl)
	}
	// Trailing zone after the last decl. No syntactic owner; treat
	// as file scope so file-only decorators stay visible while
	// decl-only ones are correctly hidden.
	return semantic.LvlFile
}

// cursorInsideDeclBody walks the token stream from the start of
// prev until the cursor and tracks brace depth. A positive count
// means the cursor sits between an opening `{` and its matching
// `}` — i.e. inside the decl body — which is the only signal we
// have without explicit End positions on AST nodes.
func cursorInsideDeclBody(view snapshotView, pos protocol.Position, prev ast.Decl) bool {
	if prev == nil {
		return false
	}
	cursorLine := int(pos.Line) + 1
	cursorCol := int(pos.Character) + 1
	startLine := prev.DeclPos().Line
	depth := 0
	for _, t := range view.tokens {
		if t.Pos.Line < startLine {
			continue
		}
		if t.Pos.Line > cursorLine || (t.Pos.Line == cursorLine && t.Pos.Column > cursorCol) {
			break
		}
		switch t.Kind {
		case lexer.LBrace:
			depth++
		case lexer.RBrace:
			depth--
		}
	}
	return depth > 0
}

// declSiteLevel maps a top-level declaration to the decorator-site
// bit it accepts. Used to filter the completion popup to decorators
// legal on the decl currently being authored.
func declSiteLevel(d ast.Decl) semantic.Level {
	switch d.(type) {
	case *ast.TypeDecl:
		return semantic.LvlType
	case *ast.EnumDecl:
		return semantic.LvlEnum
	case *ast.ErrorDecl:
		return semantic.LvlError
	case *ast.ScalarDecl:
		return semantic.LvlScalar
	case *ast.MiddlewareDecl:
		return semantic.LvlMiddleware
	case *ast.ServiceDecl:
		return semantic.LvlService
	}
	return 0
}

