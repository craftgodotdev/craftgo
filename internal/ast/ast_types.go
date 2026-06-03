// AST: TypeRef family + QualifiedIdent.
package ast

import (
	"strings"
)

type QualifiedIdent struct {
	Pos   Pos
	Parts []string
}

// String returns the dotted form, e.g. `pkg.Name` or `Name`.
func (q *QualifiedIdent) String() string { return strings.Join(q.Parts, ".") }

// TypeRef describes a type expression. Exactly one of Map or Named is set;
// Array and Optional are independent suffix flags so `T[]?` is legal.
//
// `ArrayDepth` is the number of trailing `[]` suffixes parsed (0 =
// not an array). `Array bool` is a derived convenience for call
// sites that only care whether the field is "any kind of array" -
// it equals `ArrayDepth > 0` after every parse.
type TypeRef struct {
	Pos      Pos
	Map      *MapType
	Named    *NamedTypeRef
	Array    bool
	Optional bool
	// ArrayDepth captures multi-dimensional arrays (`Tag[][]` →
	// 2). Single-dim arrays use depth 1. Code that only needs
	// "is this an array?" can keep checking [Array] / `> 0`
	// equivalently.
	ArrayDepth int
}

// ElemTypeRef returns a copy of t with ONE array dimension peeled: the depth
// is decremented and Array re-set while an inner dimension remains, so a
// multi-dimensional element keeps its array shape. Optional is dropped — the
// `?` belongs to the outer field, not each element. Returns nil for a nil
// receiver. The semantic type-checker (literal type-fit) and the codegen
// default pre-fill both peel array elements this way; sharing one definition
// keeps them from drifting.
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

// MapType represents `map<K, V>`. Both Key and Value are recursive [TypeRef]
// values so that nested maps and generic instances work uniformly.
type MapType struct {
	Pos   Pos
	Key   *TypeRef
	Value *TypeRef
}

// NamedTypeRef references a declared type, possibly with generic arguments.
// Args is non-empty only for generic instances; the codegen renames such
// instances to e.g. `FooOfUserAndOrg`.
type NamedTypeRef struct {
	Pos  Pos
	Name *QualifiedIdent
	Args []*TypeRef
}
