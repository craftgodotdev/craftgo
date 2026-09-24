package semantic

import "github.com/craftgodotdev/craftgo/internal/ast"

// requestFields returns the fields of m's request type, mixins included,
// spelled as view, the package declaring the type, spells them; ok is false
// when m has no request or it names no type.
func (a *analyzer) requestFields(m *ast.Method) (view string, fields []FlatField, ok bool) {
	if m == nil || m.Request == nil {
		return "", nil, false
	}
	pkg, sym := a.proj.resolve(a.pkg.Name, m.Request.Name)
	if pkg == nil || pkg.Types[sym] == nil {
		return "", nil, false
	}
	td := pkg.Types[sym]
	fields, _ = a.proj.flattenFields(pkg.Name, pkg.Name, td.Body, td.TypeParams, nil)
	return pkg.Name, fields, true
}
