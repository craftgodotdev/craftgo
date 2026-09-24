package ast

// Fields returns the fields of a type or error body in source order; mixins
// and comments are skipped.
func Fields(body []TypeMember) []*Field {
	var out []*Field
	for _, m := range body {
		if f, ok := m.(*Field); ok {
			out = append(out, f)
		}
	}
	return out
}

// WalkNamedRefs calls fn on every named type t reaches, through map keys and
// values and generic arguments, each after the named types in its arguments.
func (t *TypeRef) WalkNamedRefs(fn func(*NamedTypeRef)) {
	if t == nil {
		return
	}
	if t.Map != nil {
		t.Map.Key.WalkNamedRefs(fn)
		t.Map.Value.WalkNamedRefs(fn)
		return
	}
	t.Named.WalkNamedRefs(fn)
}

// WalkNamedRefs is [TypeRef.WalkNamedRefs] from n itself.
func (n *NamedTypeRef) WalkNamedRefs(fn func(*NamedTypeRef)) {
	if n == nil {
		return
	}
	for _, a := range n.Args {
		a.WalkNamedRefs(fn)
	}
	fn(n)
}
