package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkFilePosition rejects a `file` field below the top level of a request
// type, which the multipart binder never reaches, and any `file[][]`.
func (a *analyzer) checkFilePosition() {
	bodies := map[string][]ast.TypeMember{}
	hasFile := false
	record := func(name string, body []ast.TypeMember) {
		bodies[name] = body
		for _, f := range ast.Fields(body) {
			if !isFileTypeRef(f.Type) {
				continue
			}
			hasFile = true
			if f.Type.ArrayDepth > 1 {
				a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
					"field %s.%s: a multi-dimensional `file` array (`file[][]`) has no multipart encoding - only a single `file` or a 1-D `file[]` is supported", name, f.Name)
			}
		}
	}
	for _, td := range a.pkg.Types {
		record(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		record(ed.Name, ed.Body)
	}
	if !hasFile {
		return
	}

	reported := map[*ast.Field]bool{}
	report := func(f *ast.Field, owner, path string) {
		if reported[f] {
			return
		}
		reported[f] = true
		a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
			"field %s.%s: a `file` field nested inside a request body (reached through %s) is not bindable - the multipart binder reads only top-level request fields; move the `file` to the top level of the request type (or carry it in via a mixin)", owner, f.Name, path)
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			if m.Request != nil && m.Request.Name != nil {
				a.walkRequestForNestedFiles(m.Request.Name.String(), bodies, report)
			}
		}
	}
}

// walkRequestForNestedFiles reports every `file` field reached from request
// type reqName through a struct-typed field; mixin fields count as top level.
func (a *analyzer) walkRequestForNestedFiles(reqName string, bodies map[string][]ast.TypeMember, report func(f *ast.Field, owner, path string)) {
	seen := map[string]bool{}
	var nested func(owner string, members []ast.TypeMember, path string)
	nested = func(owner string, members []ast.TypeMember, path string) {
		if seen[owner] {
			return
		}
		seen[owner] = true
		for _, m := range members {
			switch v := m.(type) {
			case *ast.Field:
				if isFileTypeRef(v.Type) {
					report(v, owner, path)
					continue
				}
				for _, n := range namedTypeRefs(v.Type) {
					if n != "file" {
						nested(n, bodies[n], path+"."+v.Name)
					}
				}
			case *ast.Mixin:
				if v.Ref != nil && v.Ref.Name != nil {
					name := v.Ref.Name.String()
					nested(name, bodies[name], path)
				}
			}
		}
	}
	var top func(members []ast.TypeMember)
	top = func(members []ast.TypeMember) {
		for _, m := range members {
			switch v := m.(type) {
			case *ast.Field:
				if isFileTypeRef(v.Type) {
					continue
				}
				for _, n := range namedTypeRefs(v.Type) {
					if n != "file" {
						nested(n, bodies[n], reqName+"."+v.Name)
					}
				}
			case *ast.Mixin:
				if v.Ref != nil && v.Ref.Name != nil {
					top(bodies[v.Ref.Name.String()])
				}
			}
		}
	}
	top(bodies[reqName])
}

// isFileTypeRef reports whether t names `file`, optional or in an array.
func isFileTypeRef(t *ast.TypeRef) bool {
	return t != nil && t.Named != nil && t.Named.Name != nil && t.Named.Name.String() == "file"
}
