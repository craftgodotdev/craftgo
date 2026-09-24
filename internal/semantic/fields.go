package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// promotedField is a field and the package of its declaring type, where
// the field's bare type names resolve.
type promotedField struct {
	Field *ast.Field
	Pkg   string
}

// promotedFields returns body's own fields and those its mixins promote,
// recursively, in body order; each mixin type is visited once. A bare mixin
// resolves in its embedding type's package, homePkg at the top. incomplete
// reports a mixin that names no type, so fields may be missing.
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
// first field of a name in body order wins.
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

// requestFields returns m's request type and its promoted fields; the type
// is nil when m has no request or it does not resolve.
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
