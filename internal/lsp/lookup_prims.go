package lsp

import (
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// primsAt returns the primitive category of the field or scalar the cursor
// decorates at level, the field's type resolved in the loaded project; else 0.
func (r *request) primsAt(level semantic.Level, c cursor) semantic.Prims {
	switch level {
	case semantic.LvlField, semantic.LvlErrorField:
		if f := fieldAtCursor(r.view(), c); f != nil {
			v := r.project()
			return semantic.ResolveField(f, v.proj.Packages[v.currentPackage()], v.proj).Prims()
		}
	case semantic.LvlScalar:
		if sd := scalarAt(r.view(), c); sd != nil {
			return semantic.ScalarPrims(sd)
		}
	}
	return 0
}

// scalarAt returns the scalar on the cursor's line, or the first one below
// the cursor's decorator lines; else nil.
func scalarAt(view snapshotView, c cursor) *ast.ScalarDecl {
	if view.file == nil {
		return nil
	}
	for _, d := range view.file.Decls {
		sd, ok := d.(*ast.ScalarDecl)
		if !ok {
			continue
		}
		if sd.Pos.Line == c.line || (sd.Pos.Line >= c.line && noDeclBetween(view.file, c.line, sd.Pos.Line)) {
			return sd
		}
	}
	return nil
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
