package ast

// Equal helpers - semantic equality across AST nodes for use in test
// assertions. Comparison ignores positions (lexer.Position fields) so a
// hand-built expected value in a test doesn't have to mirror parser
// pos info; only the SHAPE of the tree matters.
//
// Convention: every method handles `nil` on both sides - `a.Equal(nil)`
// returns true iff `a` is also nil - so tests can compare fields that
// may be absent without nil-guarding at every callsite.

// Equal reports whether two QualifiedIdent reference the same dotted name.
func (q *QualifiedIdent) Equal(o *QualifiedIdent) bool {
	if q == nil || o == nil {
		return q == o
	}
	if len(q.Parts) != len(o.Parts) {
		return false
	}
	for i := range q.Parts {
		if q.Parts[i] != o.Parts[i] {
			return false
		}
	}
	return true
}

// Equal reports whether two TypeRefs describe the same type shape:
// same Map / Named / Array / Optional / ArrayDepth.
func (t *TypeRef) Equal(o *TypeRef) bool {
	if t == nil || o == nil {
		return t == o
	}
	if t.Array != o.Array || t.Optional != o.Optional {
		return false
	}
	if t.ArrayDepth != o.ArrayDepth {
		return false
	}
	if !t.Map.Equal(o.Map) {
		return false
	}
	return t.Named.Equal(o.Named)
}

// Equal reports whether two MapTypes share the same key/value shape.
func (m *MapType) Equal(o *MapType) bool {
	if m == nil || o == nil {
		return m == o
	}
	return m.Key.Equal(o.Key) && m.Value.Equal(o.Value)
}

// Equal reports whether two NamedTypeRefs share the same qualified name
// AND generic arguments (recursively).
func (n *NamedTypeRef) Equal(o *NamedTypeRef) bool {
	if n == nil || o == nil {
		return n == o
	}
	if !n.Name.Equal(o.Name) {
		return false
	}
	if len(n.Args) != len(o.Args) {
		return false
	}
	for i := range n.Args {
		if !n.Args[i].Equal(o.Args[i]) {
			return false
		}
	}
	return true
}

// Equal reports whether two Fields have the same Name + Type. Decorators
// and doc/comment are NOT compared - tests asserting decorator shape
// build smaller comparisons; tree-level equality cares about wire shape.
func (f *Field) Equal(o *Field) bool {
	if f == nil || o == nil {
		return f == o
	}
	return f.Name == o.Name && f.Type.Equal(o.Type)
}

// Equal reports whether two Mixins reference the same type (qualified
// or generic). Doc/decorators ignored - mixins carry no decorators
// anyway by parser contract.
func (m *Mixin) Equal(o *Mixin) bool {
	if m == nil || o == nil {
		return m == o
	}
	return m.Ref.Equal(o.Ref)
}

// MemberEqual reports whether two TypeMembers (Field OR Mixin) are
// equivalent. The interface itself has no Equal method (would require
// every implementer to know about every other), so callers route
// through this helper. Type-asserting both to the same concrete and
// delegating to the typed Equal keeps the dispatch in one place.
func MemberEqual(a, b TypeMember) bool {
	switch x := a.(type) {
	case *Field:
		y, ok := b.(*Field)
		return ok && x.Equal(y)
	case *Mixin:
		y, ok := b.(*Mixin)
		return ok && x.Equal(y)
	}
	return a == b
}

// MembersEqual is the slice form. Tests compare expected vs got body
// in a single call instead of looping per element.
func MembersEqual(a, b []TypeMember) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !MemberEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
