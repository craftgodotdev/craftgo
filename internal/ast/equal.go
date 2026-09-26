package ast

// The Equal methods compare shape and ignore positions; nil equals only nil.

// Equal reports whether q and o spell the same dotted name.
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

// Equal reports whether t and o describe the same type.
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

// Equal reports whether m and o have equal key and value types.
func (m *MapType) Equal(o *MapType) bool {
	if m == nil || o == nil {
		return m == o
	}
	return m.Key.Equal(o.Key) && m.Value.Equal(o.Value)
}

// Equal reports whether n and o have the same name and type arguments.
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

// Equal reports whether f and o have the same name and type, ignoring
// decorators and docs.
func (f *Field) Equal(o *Field) bool {
	if f == nil || o == nil {
		return f == o
	}
	return f.Name == o.Name && f.Type.Equal(o.Type)
}

// Equal reports whether m and o reference the same type.
func (m *Mixin) Equal(o *Mixin) bool {
	if m == nil || o == nil {
		return m == o
	}
	return m.Ref.Equal(o.Ref)
}

// MemberEqual reports whether a and b are equal fields or equal mixins; other
// members compare by identity.
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

// MembersEqual reports whether a and b are pairwise [MemberEqual].
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
