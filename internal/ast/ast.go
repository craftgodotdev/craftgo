// Package ast defines the abstract syntax tree produced by the parser.
//
// Every node carries a [Pos] (alias for [lexer.Position]) so diagnostics from
// later stages - semantic analysis, codegen, formatter, LSP - can map back to
// the originating source location. The AST is the single shared
// representation consumed by every downstream tool: keeping it small and
// strongly-typed lets the parser, semantic analyser, and codegen evolve
// independently.
package ast

import (
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Pos aliases [lexer.Position] to keep ast/* free of a hard dependency on
// lexer naming and to make node-position fields read clearly.
type Pos = lexer.Position

// Comment aliases [lexer.Comment] so consumers of the AST (codegen, lint
// tools, the formatter) can refer to one canonical type without pulling
// the lexer package into every import block.
type Comment = lexer.Comment

// CommentKind aliases [lexer.CommentKind] for the same reason.
type CommentKind = lexer.CommentKind

// astMarker is the body of every marker method (`declNode`,
// `typeMember`, `exprNode`). Marker methods exist only to seal a Go
// interface - they have no behaviour. Its one no-op assignment gives
// each marker a measurable statement, so `go tool cover` records the
// method as covered when callers exercise it rather than reporting an
// empty body as 0.0%-covered.
func astMarker() { _ = astMarkerCalled }

// astMarkerCalled is the throwaway sink that gives [astMarker] a
// statement to instrument. The boolean is otherwise unused.
var astMarkerCalled bool

type File struct {
	// LeadingDoc preserves a `//` block at the very top of the file
	// when the first AST-bearing token is a file-level decorator
	// (`@version`, `@doc`, ...). [Decorator] has no Doc field, so
	// without LeadingDoc the lexer-attached comment would be lost
	// after the parse / format round trip. When the first token is
	// `package`, the same comment lands on [PackageDecl.Doc] instead
	// and LeadingDoc stays empty.
	LeadingDoc []string
	Decorators []*Decorator
	Package    *PackageDecl
	Imports    []*Import
	Decls      []Decl
	// Comments is the side channel containing every `//` comment in
	// the source, in source order, with leading/trailing kind. The
	// parser populates it from the lexer's accumulated set so tools
	// (formatter, linters, doc generators) can read every comment
	// without re-scanning the source. Decl-level Doc/TrailingDoc
	// fields are convenience copies of comments that AST attachment
	// already captured; this slice is the exhaustive view.
	Comments []*Comment
}

// PackageDecl is the `package <name>` line. Optional in single-file projects;
// required when more than one file participates in the same logical package.
type PackageDecl struct {
	Pos  Pos
	Doc  []string
	Name string
}

// Import models a single `import [alias] "path"` line. Alias is empty when
// omitted; semantic phase derives a default alias from the last path segment.
//
// Doc captures the run of `//` comments immediately above the `import`
// keyword. TrailingDoc captures a `// note` on the same line as the path
// string, e.g. `import "auth"  // for AuthRequired middleware`.
type Import struct {
	Pos         Pos
	Alias       string
	Path        string
	Doc         []string
	TrailingDoc string
}

// Decl is the interface implemented by every top-level declaration node
// ([TypeDecl], [EnumDecl], [ErrorDecl], [ScalarDecl], [MiddlewareDecl],
// [ServiceDecl]). Use a type switch on Decl to dispatch.
type Decl interface {
	declNode()
	// DeclName returns the declared identifier (used for uniqueness checks).
	DeclName() string
	// DeclPos returns the position of the declaration keyword.
	DeclPos() Pos
}

// TypeDecl is `type Name[<TypeParams>] { Body }`. TypeParams is non-empty
// only for generic declarations (e.g. `Page<T>`); concrete instances are
// represented inline at the call site via [NamedTypeRef.Args].
//
// TrailingDoc captures a `// note` that sits on the same source line as
// the closing `}` of the body, e.g. `} // end of User`. Empty when no
// such trailing comment was present.
