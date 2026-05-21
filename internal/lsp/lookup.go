package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/config"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// snapshot is the shared parse view that every feature handler operates
// on. Re-tokenising and re-parsing on every request keeps the wire model
// simple and matches what `craftgo lint` would see - no risk of stale
// AST drift between editor and CLI.
type snapshotView struct {
	src    string
	tokens []lexer.Token
	file   *ast.File
}

func parseSnapshot(filename, src string) snapshotView {
	toks := lexer.New(filename, src).Tokenize()
	f := parser.New(filename, src).Parse()
	return snapshotView{src: src, tokens: toks, file: f}
}

// tokenAt returns the token whose source span covers the supplied
// LSP-style (0-indexed) cursor and the index into the token slice. The
// hit token may be EOF for cursors past the last real token; callers
// should check Kind to filter that out.
func (v snapshotView) tokenAt(line, character uint32) (int, lexer.Token) {
	target := lexer.Position{Line: int(line) + 1, Column: int(character) + 1}
	best := -1
	for i, t := range v.tokens {
		if t.Kind == lexer.EOF {
			continue
		}
		if t.Pos.Line != target.Line {
			continue
		}
		end := t.Pos.Column + len(t.Text)
		if t.Pos.Column <= target.Column && target.Column <= end {
			best = i
		}
	}
	if best < 0 {
		return -1, lexer.Token{}
	}
	return best, v.tokens[best]
}

// rangeOf returns the LSP range covering t.
func rangeOf(t lexer.Token) protocol.Range {
	start := lspPos(t.Pos)
	endPos := t.Pos
	endPos.Column += len(t.Text)
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// rangeOfPosLen builds a range starting at p with width n columns.
func rangeOfPosLen(p lexer.Position, n int) protocol.Range {
	start := lspPos(p)
	endPos := p
	endPos.Column += n
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// findDecl returns the first top-level declaration whose declared name
// matches. Cross-package lookups are not handled here - the caller can
// inspect the import list separately if needed.
func findDecl(f *ast.File, name string) ast.Decl {
	if f == nil {
		return nil
	}
	for _, d := range f.Decls {
		if d.DeclName() == name {
			return d
		}
	}
	return nil
}

// findDeclKindAware returns the in-file decl whose name matches and whose
// kind is appropriate for the supplied lookup context. The semantic layer
// keeps middleware names in a separate namespace from types/enums/errors/
// scalars, so a name like `AuthRequired` can legally be both an `error`
// AND a `middleware` decl. Without context, a click in
// `@middlewares(AuthRequired)` would land on whichever decl came first
// in source order - this helper restricts the search by the kind the
// surrounding syntax is documented to accept.
//
// ctx values:
//   - "middlewares": cursor inside `@middlewares(...)` - return MiddlewareDecl
//   - "errors":      cursor inside `@errors(...)`      - return ErrorDecl
//   - "type":        cursor in a type / mixin / field-type / request /
//     response / generic-arg position - return anything EXCEPT
//     MiddlewareDecl (the only decl kind that does not appear in
//     type-shape positions). This handles the inverse ambiguity:
//     `type X { AuthRequired }` should not jump to a middleware decl.
//   - "":            unknown context - returns nil so the caller falls
//     back to the generic [findDecl].
//
// Returns nil when no matching decl of the right kind exists in this
// file; callers should fall back to the generic [findDecl].
func findDeclKindAware(f *ast.File, name, ctx string) ast.Decl {
	if f == nil || ctx == "" {
		return nil
	}
	matches := makeKindMatcher(ctx)
	for _, d := range f.Decls {
		if d.DeclName() == name && matches(d) {
			return d
		}
	}
	return nil
}

// projectAST is one parsed `.craftgo` file collected from a design root.
// path is the absolute filesystem path; file is the parser output (may
// be a partial AST if the parse hit recoverable errors).
type projectAST struct {
	path string
	file *ast.File
}

// projectASTs walks upward from currentPath to find a craftgo project
// root, then parses every `.craftgo` file beneath it. The current
// buffer's text is preferred over its on-disk content so unsaved edits
// are reflected in cross-file lookups (hover, go-to-def, references).
//
// Returns nil when no project root can be found - callers should
// fall back to single-file behaviour in that case.
func (s *Server) projectASTs(currentPath, currentSrc string) []projectAST {
	files, _ := s.projectFilesWithRoot(currentPath, currentSrc)
	return files
}

// projectFilesWithRoot is the alias-aware variant of [projectASTs] that
// also returns the design-root directory. Callers that need to resolve
// `import alias "from/x/y"` paths back to filesystem locations require
// the root, so this is the canonical entry point for hover / def /
// references; [projectASTs] is kept as a thin wrapper for callers that
// only need the file list.
func (s *Server) projectFilesWithRoot(currentPath, currentSrc string) ([]projectAST, string) {
	if currentPath == "" {
		return nil, ""
	}
	_, _, designDir, err := config.Find(filepath.Dir(currentPath))
	if err != nil {
		return nil, ""
	}
	var out []projectAST
	_ = filepath.WalkDir(designDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".craftgo" {
			return nil
		}
		src := s.readFile(p, currentPath, currentSrc)
		if src == "" {
			return nil
		}
		f := parser.New(p, src).Parse()
		out = append(out, projectAST{path: p, file: f})
		return nil
	})
	return out, designDir
}

// findDeclAcross resolves a (possibly qualified) name against a set of
// already-parsed project files. The qualifier matches in priority order:
//
//  1. An alias from currentImports (`import x "from/x/y/z"` → `x.Type`).
//  2. The implicit alias derived from the last segment of an unaliased
//     import path (`import "from/x/y/z"` → `z.Type`).
//  3. A direct `package` declaration with the same name (the simple
//     `import "users"` case where path == package name).
//
// designRoot is needed so alias resolution can map an import path
// (relative to the design folder) back to the on-disk directory whose
// files we should search. Pass empty string when the lookup is
// in-package only and bare names are sufficient.
func findDeclAcross(files []projectAST, name string, currentImports []*ast.Import, designRoot string) (ast.Decl, projectAST, bool) {
	return findDeclAcrossKindAware(files, name, currentImports, designRoot, "")
}

// findDeclAcrossKindAware mirrors [findDeclAcross] with the additional
// kind filter consumed by [findDeclKindAware]. Pass ctx="" to disable
// the filter (legacy behaviour); pass "middlewares" / "errors" / "type"
// to restrict matches to the expected decl kind. The filter is essential
// when the same name lives in two decl namespaces across the project
// (e.g. `middleware AuthRequired` in one file, `error AuthRequired` in
// another) - without it the linear scan returns whichever appeared
// first, which is wrong for a click in `@middlewares(...)`.
func findDeclAcrossKindAware(files []projectAST, name string, currentImports []*ast.Import, designRoot, ctx string) (ast.Decl, projectAST, bool) {
	pkgQualifier := ""
	bare := name
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			pkgQualifier = name[:i]
			bare = name[i+1:]
			break
		}
	}
	kindMatch := makeKindMatcher(ctx)
	if pkgQualifier == "" {
		for _, p := range files {
			if p.file == nil {
				continue
			}
			for _, d := range p.file.Decls {
				if d.DeclName() == bare && kindMatch(d) {
					return d, p, true
				}
			}
		}
		return nil, projectAST{}, false
	}
	// Alias-based resolution: walk the current file's imports and
	// pick whichever alias (explicit or implicit) matches the
	// qualifier. The matched import's Path tells us which directory
	// to search.
	targetDir := ""
	for _, imp := range currentImports {
		if imp == nil {
			continue
		}
		if imp.Alias == pkgQualifier {
			targetDir = importTargetDir(designRoot, imp.Path)
			break
		}
		if imp.Alias == "" && lastPathSegment(imp.Path) == pkgQualifier {
			targetDir = importTargetDir(designRoot, imp.Path)
			break
		}
	}
	if targetDir != "" {
		for _, p := range files {
			if !inDir(p.path, targetDir) {
				continue
			}
			if p.file == nil {
				continue
			}
			for _, d := range p.file.Decls {
				if d.DeclName() == bare && kindMatch(d) {
					return d, p, true
				}
			}
		}
	}
	// Fallback: match by Package declaration. Handles the simple
	// `import "users"` case where the path equals the package name
	// AND files outside the project (no design root) but in the same
	// AST set.
	for _, p := range files {
		if p.file == nil || p.file.Package == nil || p.file.Package.Name != pkgQualifier {
			continue
		}
		for _, d := range p.file.Decls {
			if d.DeclName() == bare && kindMatch(d) {
				return d, p, true
			}
		}
	}
	return nil, projectAST{}, false
}

// makeKindMatcher returns a predicate that tests whether a decl matches
// the lookup context ctx. The empty context accepts every decl - that
// is how legacy callers (no context-aware lookup) pass through.
func makeKindMatcher(ctx string) func(ast.Decl) bool {
	switch ctx {
	case "middlewares":
		return func(d ast.Decl) bool {
			_, ok := d.(*ast.MiddlewareDecl)
			return ok
		}
	case "errors":
		return func(d ast.Decl) bool {
			_, ok := d.(*ast.ErrorDecl)
			return ok
		}
	case "type":
		return func(d ast.Decl) bool {
			_, isMW := d.(*ast.MiddlewareDecl)
			return !isMW
		}
	default:
		return func(ast.Decl) bool { return true }
	}
}

// importTargetDir resolves an import path (slash-separated, relative to
// the design root) to an absolute filesystem directory. Returns empty
// when designRoot is unset.
func importTargetDir(designRoot, importPath string) string {
	if designRoot == "" || importPath == "" {
		return ""
	}
	return filepath.Join(designRoot, filepath.FromSlash(importPath))
}

// lastPathSegment returns the trailing component of a slash-separated
// import path. Used as the implicit alias when an import has no
// explicit `alias` keyword.
func lastPathSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// inDir reports whether path lies directly inside (or equals) dir. The
// comparison is path-aware so `/proj/users` does NOT match a target of
// `/proj/use`.
func inDir(path, dir string) bool {
	clean := filepath.Clean(path)
	dirClean := filepath.Clean(dir)
	if filepath.Dir(clean) == dirClean {
		return true
	}
	return false
}

// fieldPrimAt returns the primitive category of the field at the
// cursor's source line, when the cursor is inside a type / error
// body. The category drives the AppliesTo filter on `@<decorator>`
// completion: a `total int? @<cursor>` should only see number-side
// validators, not string-side or array-side ones.
//
// Returns 0 (PrimAny) when the cursor is not inside a recognised
// field row - caller treats that as "no AppliesTo filter".
func fieldPrimAt(view snapshotView, pos protocol.Position) semantic.Prims {
	if view.file == nil {
		return 0
	}
	line := int(pos.Line) + 1
	for _, d := range view.file.Decls {
		body, ok := declBody(d)
		if !ok {
			continue
		}
		for _, m := range body {
			f, ok := m.(*ast.Field)
			if !ok || f.Pos.Line != line {
				continue
			}
			return primOfTypeRef(f.Type, view.file)
		}
	}
	return 0
}

// scalarPrimAt resolves the underlying primitive category for a scalar
// declaration the cursor sits on. Walks the file's top-level decls
// looking for a ScalarDecl whose position is on the same line as the
// cursor (or whose decorator chain reaches the cursor's line). Returns
// 0 (PrimAny) when the cursor is not inside a scalar context, so the
// caller skips the AppliesTo filter cleanly.
//
// Used by `@<cursor>` completion at LvlScalar to drop decorators
// whose AppliesTo bit does not intersect the scalar's primitive -
// otherwise typing `scalar Gmail string @<cursor>` would offer
// number-only validators like `@gt` that the semantic phase would
// later reject as a type mismatch.
func scalarPrimAt(view snapshotView, pos protocol.Position) semantic.Prims {
	if view.file == nil {
		return 0
	}
	line := int(pos.Line) + 1
	for _, d := range view.file.Decls {
		sd, ok := d.(*ast.ScalarDecl)
		if !ok {
			continue
		}
		// Match scalars on the cursor's own line OR scalars sitting
		// just below decorator lines the user is currently editing
		// (the "decorator zone above the decl"). Same heuristic as
		// guessLevel.
		if sd.Pos.Line == line || (sd.Pos.Line >= line && noDeclBetween(view.file, line, sd.Pos.Line)) {
			return primFromIdent(sd.Primitive)
		}
	}
	return 0
}

// noDeclBetween reports whether the file has zero declarations on
// lines strictly between `from` (exclusive) and `to` (exclusive). Used
// by scalarPrimAt to make sure the "above" attribution stays adjacent.
func noDeclBetween(f *ast.File, from, to int) bool {
	for _, d := range f.Decls {
		l := d.DeclPos().Line
		if l > from && l < to {
			return false
		}
	}
	return true
}

// primFromIdent maps a built-in primitive spelling to its semantic
// category bit. Kept local to the LSP layer (mirroring semantic's own
// primFromName) so this package does not reach into semantic for an
// unexported helper.
func primFromIdent(name string) semantic.Prims {
	switch name {
	case "string", "bytes":
		return semantic.PrimString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return semantic.PrimNumber
	case "bool":
		return semantic.PrimBool
	case "file":
		return semantic.PrimFile
	}
	return 0
}

// fieldAtCursor returns the field whose row matches the cursor's line
// when the cursor is inside a type / error body. Returns nil when the
// cursor is not on a field row.
func fieldAtCursor(view snapshotView, pos protocol.Position) *ast.Field {
	if view.file == nil {
		return nil
	}
	line := int(pos.Line) + 1
	for _, d := range view.file.Decls {
		body, ok := declBody(d)
		if !ok {
			continue
		}
		for _, m := range body {
			f, ok := m.(*ast.Field)
			if !ok || f.Pos.Line != line {
				continue
			}
			return f
		}
	}
	return nil
}

// declBody returns a type-body slice for declarations that have one
// (TypeDecl always; ErrorDecl when HasBody is set). The bool says
// whether a body exists; nil-body decls return false so callers can
// short-circuit cleanly.
func declBody(d ast.Decl) ([]ast.TypeMember, bool) {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return v.Body, true
	case *ast.ErrorDecl:
		if v.HasBody {
			return v.Body, true
		}
	}
	return nil, false
}

// primOfTypeRef reduces a TypeRef to its primitive bucket.
//
// Array and map fields collapse to [semantic.PrimArray] regardless of
// their element type - that's the bucket the array-level decorators
// (`@minItems`, `@maxItems`, `@uniqueItems`) check against. Optional
// (`?`) is transparent: `int?` is still PrimNumber.
//
// User scalars look up the scalar's primitive (recursively, in case a
// scalar references another scalar). Unknown / cross-package refs
// return 0 so the caller falls back to "no AppliesTo filter" rather
// than hiding decorators we cannot classify.
func primOfTypeRef(t *ast.TypeRef, file *ast.File) semantic.Prims {
	if t == nil {
		return 0
	}
	if t.Array || t.Map != nil {
		return semantic.PrimArray
	}
	if t.Named == nil {
		return 0
	}
	name := t.Named.Name.String()
	switch name {
	case "string", "bytes":
		return semantic.PrimString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return semantic.PrimNumber
	case "bool":
		return semantic.PrimBool
	case "file":
		return semantic.PrimFile
	case "any", "object":
		return 0
	}
	if file != nil {
		for _, d := range file.Decls {
			if sd, ok := d.(*ast.ScalarDecl); ok && sd.Name == name {
				// Synthesise a TypeRef around the scalar's primitive
				// name and recurse so the lookup transparently
				// handles scalar-of-scalar chains.
				inner := &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{sd.Primitive}}}}
				return primOfTypeRef(inner, file)
			}
		}
	}
	return 0
}

// pathToFileURIString builds a `file://...` URI string from an absolute
// filesystem path. Feature handlers feed the result into [uri.New] so
// the typed URI lines up with what the editor would have sent for a
// sibling file in the same project.
func pathToFileURIString(path string) string {
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}
	return "file://" + abs
}

// declSummary renders a short one-line signature for d, suitable for the
// header of a hover popup. It mirrors the canonical formatter style so
// editors and `craftgo fmt` agree on what the construct looks like.
func declSummary(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		s := "type " + v.Name
		if len(v.TypeParams) > 0 {
			s += "<"
			for i, tp := range v.TypeParams {
				if i > 0 {
					s += ", "
				}
				s += tp
			}
			s += ">"
		}
		return s
	case *ast.EnumDecl:
		return "enum " + v.Name
	case *ast.ErrorDecl:
		return "error " + v.Category + " " + v.Name
	case *ast.ScalarDecl:
		return "scalar " + v.Name + " " + v.Primitive
	case *ast.MiddlewareDecl:
		return "middleware " + v.Name
	case *ast.ServiceDecl:
		if v.Extend {
			return "extend service " + v.Name
		}
		return "service " + v.Name
	}
	return ""
}

// declDoc returns the doc-comment lines of d. Every doc-bearing decl
// type is enumerated here so hover popups stay consistent across
// type / enum / error / service / scalar / middleware.
func declDoc(d ast.Decl) []string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return v.Doc
	case *ast.EnumDecl:
		return v.Doc
	case *ast.ErrorDecl:
		return v.Doc
	case *ast.ServiceDecl:
		return v.Doc
	case *ast.ScalarDecl:
		return v.Doc
	case *ast.MiddlewareDecl:
		return v.Doc
	}
	return nil
}
