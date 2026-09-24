package semantic

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// FormField is one part of a multipart request.
type FormField struct {
	Field *ast.Field
	// Name is the target's identifier for the field, from [LevelNames].
	Name string
	// WireName is the multipart key, honouring an explicit `@form("k")`.
	WireName string
	Required bool
	// MimeTypes is the `@mimeTypes` allowlist; file fields only.
	MimeTypes []string
	// IsArray reports a repeated file part.
	IsArray bool
}

// FormFields splits m's body and form fields into multipart text and file
// parts; both are nil when none is a file.
func FormFields(m *ast.Method, pkg *Package, r *Resolver, levelNames LevelNames) (text, files []FormField) {
	if m == nil || m.Request == nil {
		return nil, nil
	}
	for _, rf := range RequestFields(m, pkg, r, levelNames) {
		switch rf.Binding {
		case wire.BindPath, wire.BindQuery, wire.BindHeader, wire.BindCookie, wire.BindSensitive:
			continue
		}
		f := rf.Field
		entry := FormField{
			Field:    f,
			Name:     rf.Name,
			WireName: wire.WireName(f, wire.BindForm),
			Required: rf.SpecRequired,
		}
		if isFileRef(f) {
			entry.IsArray = f.Type.Array
			entry.MimeTypes = mimeTypesOf(f.Decorators)
			files = append(files, entry)
			continue
		}
		text = append(text, entry)
	}
	if len(files) == 0 {
		return nil, nil
	}
	return text, files
}

// isFileRef reports whether the field's type is the `file` primitive.
func isFileRef(f *ast.Field) bool {
	return f != nil && f.Type != nil && f.Type.Named != nil && f.Type.Named.Name.String() == "file"
}

// mimeTypesOf reads the `@mimeTypes` allowlist.
func mimeTypesOf(ds []*ast.Decorator) []string {
	var out []string
	for _, d := range ds {
		if d == nil || d.Name != "mimeTypes" {
			continue
		}
		for _, n := range ast.ArgNames(d) {
			out = append(out, n.Value)
		}
	}
	return out
}
