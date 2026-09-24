package ast

import (
	"reflect"
	"testing"
)

func named(name string, args ...*TypeRef) *NamedTypeRef {
	return &NamedTypeRef{Name: &QualifiedIdent{Parts: []string{name}}, Args: args}
}

func TestFieldsSkipsMixinsAndComments(t *testing.T) {
	a, b := &Field{Name: "a"}, &Field{Name: "b"}
	body := []TypeMember{a, &Mixin{Ref: named("Base")}, &FreeComment{Text: []string{"// c"}}, b}
	if got := Fields(body); !reflect.DeepEqual(got, []*Field{a, b}) {
		t.Errorf("Fields = %v, want [a b]", got)
	}
	if got := Fields(nil); got != nil {
		t.Errorf("Fields(nil) = %v, want nil", got)
	}
}

func TestWalkNamedRefsVisitsArgumentsFirst(t *testing.T) {
	// map<Key, Page<Pair<A, B[]>>?>
	page := named("Page", &TypeRef{Named: named("Pair",
		&TypeRef{Named: named("A")},
		&TypeRef{Named: named("B"), Array: true, ArrayDepth: 1},
	)})
	ref := &TypeRef{Map: &MapType{
		Key:   &TypeRef{Named: named("Key")},
		Value: &TypeRef{Named: page, Optional: true},
	}}
	var got []string
	ref.WalkNamedRefs(func(n *NamedTypeRef) { got = append(got, n.Name.String()) })
	if want := []string{"Key", "A", "B", "Pair", "Page"}; !reflect.DeepEqual(got, want) {
		t.Errorf("visit order = %v, want %v", got, want)
	}

	got = nil
	page.WalkNamedRefs(func(n *NamedTypeRef) { got = append(got, n.Name.String()) })
	if want := []string{"A", "B", "Pair", "Page"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from the named ref = %v, want %v", got, want)
	}
}

func TestWalkNamedRefsNilRoots(t *testing.T) {
	fail := func(n *NamedTypeRef) { t.Errorf("unexpected visit of %v", n) }
	(*TypeRef)(nil).WalkNamedRefs(fail)
	(*NamedTypeRef)(nil).WalkNamedRefs(fail)
	(&TypeRef{Map: &MapType{}}).WalkNamedRefs(fail)
}
