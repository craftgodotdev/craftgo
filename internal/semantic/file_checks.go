// `file` placement checks: uploads must sit at the top level of a request
// body where the multipart binder can reach them.
package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkFilePosition rejects a `file` field nested below the top level of a
// request body. The form-binding codegen ([collectFormBindings]) scans only
// the resolved top-level fields of a method's request type, so a `file`
// reached through a named struct field is never bound: the request is decoded
// as JSON, the `*multipart.FileHeader` field stays nil, and the upload is
// silently lost. Both gen and `go build` succeed, so this design-time
// rejection is the only signal.
//
// Only request-side nesting is rejected. A top-level `file` field — directly
// on the request or flattened in via a mixin — is the supported upload shape.
// A `file` field that appears in a response (or in a type echoed back as a
// response, e.g. a profile that carries an avatar) is left alone: that is an
// established modelling pattern, lowered to the OpenAPI `format: binary`
// shape.
func (a *analyzer) checkFilePosition() {
	bodies := map[string][]ast.TypeMember{}
	hasFile := false
	record := func(name string, body []ast.TypeMember) {
		bodies[name] = body
		for _, m := range body {
			f, ok := m.(*ast.Field)
			if !ok || !isFileTypeRef(f.Type) {
				continue
			}
			hasFile = true
			// A single `file` lowers to one binary blob and a `file[]` to flat
			// repeated multipart parts; `file[][]` (or deeper) has no wire
			// encoding and the binder would assign a 1-D slice to a nested one.
			if f.Type.ArrayDepth > 1 {
				a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeFilePosition,
					"field %s.%s: a multi-dimensional `file` array (`file[][]`) has no multipart encoding — only a single `file` or a 1-D `file[]` is supported", name, f.Name)
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
			"field %s.%s: a `file` field nested inside a request body (reached through %s) is not bindable — the multipart binder reads only top-level request fields; move the `file` to the top level of the request type (or carry it in via a mixin)", owner, f.Name, path)
	}
	for _, si := range a.pkg.Services {
		for _, m := range si.Methods {
			if m.Request != nil && m.Request.Name != nil {
				a.walkRequestForNestedFiles(m.Request.Name.String(), bodies, report)
			}
		}
	}
}

// walkRequestForNestedFiles reports every `file` field reachable from a
// request type below its top level. Top-level fields (direct or mixin-
// flattened) are the binder's domain and are skipped; descent into a named
// struct field marks everything beneath it as nested.
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
	// Top level: a direct or mixin-flattened `file` field binds correctly, so
	// only descend into named struct fields to find nested files.
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

// isFileTypeRef reports whether t names the built-in `file` type (bare or
// optional; arrays included so a misplaced `file[]` is still caught here).
func isFileTypeRef(t *ast.TypeRef) bool {
	return t != nil && t.Named != nil && t.Named.Name != nil && t.Named.Name.String() == "file"
}
