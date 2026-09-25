package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkFilePosition rejects a `file` field below the top level of a request
// type, which the multipart binder never reaches, and any `file[][]`.
func (a *analyzer) checkFilePosition() {
	for _, td := range a.pkg.Types {
		a.checkFileArrayDepth(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		a.checkFileArrayDepth(ed.Name, ed.Body)
	}
	reported := map[lexer.Position]bool{}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			view, fields, ok := a.requestFields(m)
			if !ok {
				continue
			}
			seen := map[string]bool{}
			for _, ff := range fields {
				a.reportNestedFiles(view, ff.Field.Type, m.Request.Name.String()+"."+ff.Field.Name, seen, reported)
			}
		}
	}
}

// checkFileArrayDepth rejects a `file[][]` field of the body of owner.
func (a *analyzer) checkFileArrayDepth(owner string, body []ast.TypeMember) {
	for _, f := range ast.Fields(body) {
		if isFileTypeRef(f.Type) && f.Type.ArrayDepth > 1 {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
				"field %s.%s: a multi-dimensional `file` array (`file[][]`) has no multipart encoding - only a single `file` or a 1-D `file[]` is supported", owner, f.Name)
		}
	}
}

// reportNestedFiles reports each `file` field of the struct types t reaches,
// mixin fields included, and of the structs below them; t is spelled as
// package view spells it and path names how the request reaches it.
func (a *analyzer) reportNestedFiles(view string, t *ast.TypeRef, path string, seen map[string]bool, reported map[lexer.Position]bool) {
	t.WalkNamedRefs(func(n *ast.NamedTypeRef) {
		pkg, sym := a.proj.resolve(view, n.Name)
		if pkg == nil || pkg.Types[sym] == nil || seen[pkg.Name+"."+sym] {
			return
		}
		seen[pkg.Name+"."+sym] = true
		td := pkg.Types[sym]
		fields, _ := a.proj.flattenFields(view, pkg.Name, td.Body, td.TypeParams, nil)
		for _, ff := range fields {
			f := ff.Field
			if !isFileTypeRef(f.Type) {
				a.reportNestedFiles(view, f.Type, path+"."+f.Name, seen, reported)
				continue
			}
			if reported[f.Pos] {
				continue
			}
			reported[f.Pos] = true
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
				"field %s.%s: a `file` field nested inside a request body (reached through %s) is not bindable - the multipart binder reads only top-level request fields; move the `file` to the top level of the request type (or carry it in via a mixin)", td.Name, f.Name, path)
		}
	})
}

// isFileTypeRef reports whether t names `file`, optional or in an array.
func isFileTypeRef(t *ast.TypeRef) bool {
	return t != nil && t.Named != nil && t.Named.Name != nil && t.Named.Name.String() == "file"
}
