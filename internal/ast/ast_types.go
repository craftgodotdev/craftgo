package ast

import (
	"strings"
)

// QualifiedIdent is a dotted name such as `pkg.User`.
type QualifiedIdent struct {
	Pos   Pos
	Parts []string
}

// String returns the dotted form, e.g. `pkg.Name` or `Name`.
func (q *QualifiedIdent) String() string { return strings.Join(q.Parts, ".") }

// TypeRef is a type expression: Map or Named, then ArrayDepth `[]` suffixes and
// an optional `?`. The parser keeps Array equal to ArrayDepth > 0.
type TypeRef struct {
	Pos        Pos
	Map        *MapType
	Named      *NamedTypeRef
	Array      bool
	Optional   bool
	ArrayDepth int
}

// ElemTypeRef returns t's element type: a copy with one array dimension
// removed and no `?`. It returns nil for a nil t.
func (t *TypeRef) ElemTypeRef() *TypeRef {
	if t == nil {
		return nil
	}
	clone := *t
	clone.Array = false
	clone.Optional = false
	if clone.ArrayDepth > 0 {
		clone.ArrayDepth--
	}
	if clone.ArrayDepth > 0 {
		clone.Array = true
	}
	return &clone
}

// MapType is `map<Key, Value>`.
type MapType struct {
	Pos   Pos
	Key   *TypeRef
	Value *TypeRef
}

// NamedTypeRef names a type; Args holds the type arguments of a generic
// instance.
type NamedTypeRef struct {
	Pos  Pos
	Name *QualifiedIdent
	Args []*TypeRef
}
