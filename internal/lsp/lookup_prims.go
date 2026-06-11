// Cursor-context classification for completion / hover: the primitive
// category of the field or scalar at the cursor, and declaration
// summaries / docs for hover rendering.
package lsp

import (
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// fieldPrimAt returns the primitive category of the field at the
// cursor's source line, when the cursor is inside a type / error
// body. The category drives the AppliesTo filter on `@<decorator>`
// completion: a `total int? @<cursor>` should only see number-side
// validators, not string-side or array-side ones.
//
// Returns 0 (PrimAny) when the cursor is not inside a recognised
// field row - caller treats that as "no AppliesTo filter".
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
			return primOfTypeRef(f.Type, view.file)
		}
	}
	return 0
}

// scalarPrimAt resolves the underlying primitive category for a scalar
// declaration the cursor sits on. Walks the file's top-level decls
// looking for a ScalarDecl whose position is on the same line as the
// cursor (or whose decorator chain reaches the cursor's line). Returns
// 0 (PrimAny) when the cursor is not inside a scalar context, so the
// caller skips the AppliesTo filter cleanly.
//
// Used by `@<cursor>` completion at LvlScalar to drop decorators
// whose AppliesTo bit does not intersect the scalar's primitive -
// otherwise typing `scalar Gmail string @<cursor>` would offer
// number-only validators like `@gt` that the semantic phase would
// later reject as a type mismatch.
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
		// Match scalars on the cursor's own line OR scalars sitting
		// just below decorator lines the user is currently editing
		// (the "decorator zone above the decl"). Same heuristic as
		// guessLevel.
		if sd.Pos.Line == line || (sd.Pos.Line >= line && noDeclBetween(view.file, line, sd.Pos.Line)) {
			return primFromIdent(sd.Primitive)
		}
	}
	return 0
}

// primFromIdent maps a built-in primitive spelling to its semantic
// category bit. Delegates to [semantic.PrimFromName] so the editor's
// primitive classification can't drift from the analyser's.
func primFromIdent(name string) semantic.Prims {
	return semantic.PrimFromName(name)
}

// primOfTypeRef reduces a TypeRef to its primitive bucket.
//
// Array and map fields collapse to [semantic.PrimArray] regardless of
// their element type - that's the bucket the array-level decorators
// (`@minItems`, `@maxItems`, `@uniqueItems`) check against. Optional
// (`?`) is transparent: `int?` is still PrimNumber.
//
// User scalars look up the scalar's primitive (recursively, in case a
// scalar references another scalar). Unknown / cross-package refs
// return 0 so the caller falls back to "no AppliesTo filter" rather
// than hiding decorators we cannot classify.
func primOfTypeRef(t *ast.TypeRef, file *ast.File) semantic.Prims {
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
	switch name {
	case "string", "bytes":
		return semantic.PrimString
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return semantic.PrimNumber
	case "bool":
		return semantic.PrimBool
	case "file":
		return semantic.PrimFile
	case "any", "object":
		return 0
	}
	if file != nil {
		for _, d := range file.Decls {
			if sd, ok := d.(*ast.ScalarDecl); ok && sd.Name == name {
				// Synthesise a TypeRef around the scalar's primitive
				// name and recurse so the lookup transparently
				// handles scalar-of-scalar chains.
				inner := &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{sd.Primitive}}}}
				return primOfTypeRef(inner, file)
			}
		}
	}
	return 0
}

// declSummary renders a short one-line signature for d, suitable for the
// header of a hover popup. It mirrors the canonical formatter style so
// editors and `craftgo fmt` agree on what the construct looks like.
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
	case *ast.ServiceDecl:
		if v.Extend {
			return "extend service " + v.Name
		}
		return "service " + v.Name
	}
	return ""
}

// declDoc returns the doc-comment lines of d. Every doc-bearing decl
// type is enumerated here so hover popups stay consistent across
// type / enum / error / service / scalar / middleware.
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
	}
	return nil
}
