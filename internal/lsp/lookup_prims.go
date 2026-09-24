package lsp

import (
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// fieldPrimAt returns the primitive category of the field on the cursor's
// line, or 0 off a field row.
func fieldPrimAt(view snapshotView, c cursor) semantic.Prims {
	f := fieldAtCursor(view, c)
	if f == nil {
		return 0
	}
	return primOfTypeRef(f.Type, f.Decorators, view.file)
}

// scalarPrimAt returns the primitive category of the scalar on the cursor's
// line, or of the first one below the cursor's decorator lines; else 0.
func scalarPrimAt(view snapshotView, c cursor) semantic.Prims {
	if view.file == nil {
		return 0
	}
	for _, d := range view.file.Decls {
		sd, ok := d.(*ast.ScalarDecl)
		if !ok {
			continue
		}
		if sd.Pos.Line == c.line || (sd.Pos.Line >= c.line && noDeclBetween(view.file, c.line, sd.Pos.Line)) {
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

// declInfo is how the editor shows a declaration: its declaration line (e.g.
// `type Page<T>`, `error NotFound Missing`), its doc, its symbol kind in the
// outline and the workspace symbols, and its completion item kind.
type declInfo struct {
	summary string
	doc     []string
	symbol  protocol.SymbolKind
	item    protocol.CompletionItemKind
}

// infoOf returns how the editor shows d.
func infoOf(d ast.Decl) declInfo {
	switch v := d.(type) {
	case *ast.TypeDecl:
		summary := "type " + v.Name
		if len(v.TypeParams) > 0 {
			summary += "<" + strings.Join(v.TypeParams, ", ") + ">"
		}
		return declInfo{summary, v.Doc, protocol.SymbolKindStruct, protocol.CompletionItemKindStruct}
	case *ast.EnumDecl:
		return declInfo{"enum " + v.Name, v.Doc, protocol.SymbolKindEnum, protocol.CompletionItemKindEnum}
	case *ast.ErrorDecl:
		return declInfo{"error " + v.Category + " " + v.Name, v.Doc, protocol.SymbolKindClass, protocol.CompletionItemKindClass}
	case *ast.ScalarDecl:
		return declInfo{"scalar " + v.Name + " " + v.Primitive, v.Doc, protocol.SymbolKindClass, protocol.CompletionItemKindUnit}
	case *ast.MiddlewareDecl:
		return declInfo{"middleware " + v.Name, v.Doc, protocol.SymbolKindFunction, protocol.CompletionItemKindFunction}
	case *ast.EventDecl:
		return declInfo{"event " + v.Name, v.Doc, protocol.SymbolKindEvent, protocol.CompletionItemKindEvent}
	case *ast.ServiceDecl:
		summary := "service " + v.Name
		if v.Extend {
			summary = "extend " + summary
		}
		return declInfo{summary, v.Doc, protocol.SymbolKindInterface, protocol.CompletionItemKindInterface}
	}
	return declInfo{}
}
