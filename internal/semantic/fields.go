// The mixin-aware field view of a type body: every field the body
// contributes, directly or through a mixin, with the package a bare type
// name inside the field resolves in.
package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// promotedField is a field a type body contributes and the package its
// declaring type lives in - the package a bare type name in the field's
// type resolves in.
type promotedField struct {
	Field *ast.Field
	Pkg   string
}

// promotedFields returns every field body contributes: its own fields and
// the fields its mixins promote, recursively, in body order. homePkg is
// the package the body's declaring type lives in: a bare mixin resolves
// there, a qualified one in its own package, and a bare mixin nested
// inside a foreign mixin resolves in that mixin's package. A cycle or a
// diamond embed is visited once. incomplete reports that a mixin resolved
// to no type declaration, so the list may miss fields (the reference pass
// reports the unresolved name).
func (a *analyzer) promotedFields(homePkg string, body []ast.TypeMember) (fields []promotedField, incomplete bool) {
	incomplete = a.walkPromotedFields(homePkg, body, map[string]bool{}, &fields)
	return fields, incomplete
}

func (a *analyzer) walkPromotedFields(homePkg string, body []ast.TypeMember, visited map[string]bool, out *[]promotedField) (incomplete bool) {
	for _, m := range body {
		switch v := m.(type) {
		case *ast.Field:
			*out = append(*out, promotedField{Field: v, Pkg: homePkg})
		case *ast.Mixin:
			if v == nil || v.Ref == nil || v.Ref.Name == nil {
				continue
			}
			pkg, sym := a.resolveNamed(homePkg, v.Ref)
			if pkg == nil || pkg.Types[sym] == nil {
				incomplete = true
				continue
			}
			key := pkg.Name + "." + sym
			if visited[key] {
				continue
			}
			visited[key] = true
			if a.walkPromotedFields(pkg.Name, pkg.Types[sym].Body, visited, out) {
				incomplete = true
			}
		}
	}
	return incomplete
}

// promotedFieldSet is [analyzer.promotedFields] keyed by field name; the
// first field of a name in body order wins, so a host field declared
// before a mixin shadows the field the mixin promotes.
func (a *analyzer) promotedFieldSet(homePkg string, body []ast.TypeMember) (map[string]promotedField, bool) {
	fields, incomplete := a.promotedFields(homePkg, body)
	out := make(map[string]promotedField, len(fields))
	for _, pf := range fields {
		if _, dup := out[pf.Field.Name]; !dup {
			out[pf.Field.Name] = pf
		}
	}
	return out, incomplete
}

// requestFields resolves m's request type and returns it with its
// promoted fields. The declaration is nil when the method has no request
// or the type does not resolve.
func (a *analyzer) requestFields(m *ast.Method) (*ast.TypeDecl, []promotedField) {
	if m == nil || m.Request == nil || m.Request.Name == nil {
		return nil, nil
	}
	pkg, sym := a.resolveNamed(a.pkg.Name, m.Request)
	if pkg == nil {
		return nil, nil
	}
	td := pkg.Types[sym]
	if td == nil {
		return nil, nil
	}
	fields, _ := a.promotedFields(pkg.Name, td.Body)
	return td, fields
}
