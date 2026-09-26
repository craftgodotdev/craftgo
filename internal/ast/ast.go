// Package ast defines the syntax tree the parser builds for a craftgo file.
package ast

import (
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Pos is a node's source position.
type Pos = lexer.Position

// Comment is a `//` comment as the lexer recorded it.
type Comment = lexer.Comment

// CommentKind is [lexer.CommentKind].
type CommentKind = lexer.CommentKind

// astMarker is the body of the interface marker methods, giving each a
// statement for coverage to count.
func astMarker() { _ = astMarkerCalled }

var astMarkerCalled bool

// File is one parsed source file.
type File struct {
	// LeadingDoc is the comment above the first decorator of a file that
	// opens with one.
	LeadingDoc []string
	Decorators []*Decorator
	Package    *PackageDecl
	Imports    []*Import
	Decls      []Decl
	// FreeComments are the file-scope comment blocks no node claimed.
	FreeComments []*FreeComment
	// ChainComments maps the line of a decorator, or of the name or keyword
	// after a decorator chain, to the comment lines above it inside the chain.
	ChainComments map[int][]string
	// Comments is every comment in the file in source order, including those
	// a Doc field also holds.
	Comments []*Comment
}

// PackageDecl is the `package name` clause; Pos is the name's position.
type PackageDecl struct {
	Pos  Pos
	Doc  []string
	Name string
}

// Import is `import "path"` or `import alias "path"`. PathText is Path as
// written, quotes included; Doc is the comment above it.
type Import struct {
	Pos      Pos
	Alias    string
	Path     string
	PathText string
	Doc      []string
}

// Decl is a top-level declaration; [AllDeclKinds] has one of each kind.
type Decl interface {
	declNode()
	// DeclName returns the declared name.
	DeclName() string
	// DeclPos returns the position of the declaration keyword.
	DeclPos() Pos
	// DeclNamePos returns the position of the declared name, the extended
	// service's for `extend service`.
	DeclNamePos() Pos
}
