package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// fieldPrimAt returns the primitive category of the field on the cursor's
// line, or 0 off a field row.
func fieldPrimAt(view snapshotView, pos protocol.Position) semantic.Prims {
	if view.file == nil {
		return 0
	}
	line := int(pos.Line) + 1
	for _, d := range view.file.Decls {
		body, ok := declBody(d)
		if !ok {
			continue
		}
		for _, m := range body {
			f, ok := m.(*ast.Field)
			if !ok || f.Pos.Line != line {
				continue
			}
			return primOfTypeRef(f.Type, f.Decorators, view.file)
		}
	}
	return 0
}

// scalarPrimAt returns the primitive category of the scalar on the cursor's
// line, or of the first one below the cursor's decorator lines; else 0.
func scalarPrimAt(view snapshotView, pos protocol.Position) semantic.Prims {
	if view.file == nil {
		return 0
	}
	line := int(pos.Line) + 1
	for _, d := range view.file.Decls {
		sd, ok := d.(*ast.ScalarDecl)
		if !ok {
			continue
		}
		if sd.Pos.Line == line || (sd.Pos.Line >= line && noDeclBetween(view.file, line, sd.Pos.Line)) {
			return primFromIdent(sd.Primitive, sd.Decorators)
		}
	}
	return 0
}

// primFromIdent returns the category of a built-in name; `bytes` with
// `@format(raw)` among decs is [semantic.PrimRawBytes].
func primFromIdent(name string, decs []*ast.Decorator) semantic.Prims {
	if name == "bytes" && semantic.HasRawFormat(decs) {
		return semantic.PrimRawBytes
	}
	return semantic.PrimFromName(name)
}

// primOfTypeRef returns the category of a field type: [semantic.PrimArray] for
// an array or map, else that of its built-in or of a scalar declared in file.
func primOfTypeRef(t *ast.TypeRef, decs []*ast.Decorator, file *ast.File) semantic.Prims {
	if t == nil {
		return 0
	}
	if t.Array || t.Map != nil {
		return semantic.PrimArray
	}
	if t.Named == nil {
		return 0
	}
	name := t.Named.Name.String()
	// `any` and `object` stay unclassified.
	if p := primFromIdent(name, decs); p != 0 {
		return p
	}
	if name == "any" || name == "object" {
		return 0
	}
	if file != nil {
		for _, d := range file.Decls {
			if sd, ok := d.(*ast.ScalarDecl); ok && sd.Name == name {
				inner := &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{sd.Primitive}}}}
				if semantic.HasRawFormat(sd.Decorators) {
					// The scalar's `@format(raw)` covers every field typed with it.
					decs = sd.Decorators
				}
				return primOfTypeRef(inner, decs, file)
			}
		}
	}
	return 0
}

// declSummary renders d's declaration line, e.g. `type Page<T>` or
// `error NotFound Missing`.
func declSummary(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		s := "type " + v.Name
		if len(v.TypeParams) > 0 {
			s += "<"
			for i, tp := range v.TypeParams {
				if i > 0 {
					s += ", "
				}
				s += tp
			}
			s += ">"
		}
		return s
	case *ast.EnumDecl:
		return "enum " + v.Name
	case *ast.ErrorDecl:
		return "error " + v.Category + " " + v.Name
	case *ast.ScalarDecl:
		return "scalar " + v.Name + " " + v.Primitive
	case *ast.MiddlewareDecl:
		return "middleware " + v.Name
	case *ast.EventDecl:
		return "event " + v.Name
	case *ast.ServiceDecl:
		if v.Extend {
			return "extend service " + v.Name
		}
		return "service " + v.Name
	}
	return ""
}

// declDoc returns d's doc-comment lines.
func declDoc(d ast.Decl) []string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return v.Doc
	case *ast.EnumDecl:
		return v.Doc
	case *ast.ErrorDecl:
		return v.Doc
	case *ast.ServiceDecl:
		return v.Doc
	case *ast.ScalarDecl:
		return v.Doc
	case *ast.MiddlewareDecl:
		return v.Doc
	case *ast.EventDecl:
		return v.Doc
	}
	return nil
}
