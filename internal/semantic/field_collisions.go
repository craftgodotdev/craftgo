package semantic

import (
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkFieldNameCollisions warns when two fields of a type or error body map
// to one Go field name.
func (a *analyzer) checkFieldNameCollisions(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkOneDeclFieldCollisions(d)
		}
	}
}

func (a *analyzer) checkOneDeclFieldCollisions(d ast.Decl) {
	var (
		parent  string
		members []ast.TypeMember
	)
	switch dd := d.(type) {
	case *ast.TypeDecl:
		parent, members = "type "+dd.Name, dd.Body
	case *ast.ErrorDecl:
		parent, members = "error "+dd.Name, dd.Body
	default:
		return
	}
	a.warnFieldCollisions(parent, members)
}

// warnFieldCollisions reports every colliding field of a body but the first.
func (a *analyzer) warnFieldCollisions(parent string, members []ast.TypeMember) {
	var fields []*ast.Field
	for _, f := range ast.Fields(members) {
		if f.Name != "" {
			fields = append(fields, f)
		}
	}
	if len(fields) < 2 {
		return
	}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	_, collisions := idents.DedupGoFieldNames(names)
	if len(collisions) == 0 {
		return
	}
	byName := map[string]*ast.Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	for _, c := range collisions {
		if len(c.DSLNames) < 2 {
			continue
		}
		firstDSL := c.DSLNames[0]
		first := byName[firstDSL]
		for rank, dupeName := range c.DSLNames[1:] {
			anchor := byName[dupeName]
			if anchor == nil {
				continue
			}
			d := a.diag(anchor.Pos, anchor.Pos, lexer.SeverityWarning, CodeFieldNameCollision,
				"field name %q collides with %q in %s - both normalise to Go identifier %q; codegen will emit %q to keep the struct compilable, but the JSON wire shape carries BOTH DSL spellings (rename one if this duplication was unintended)",
				dupeName, firstDSL, parent, c.CanonicalGoName, c.ResolvedGoNames[rank+1])
			if first != nil {
				d.Related = related(first.Pos, "first declared here (keeps the canonical Go name)")
			}
		}
	}
}

// isLetterStart reports whether s begins with a Unicode letter.
func isLetterStart(s string) bool {
	for _, r := range s {
		return unicode.IsLetter(r)
	}
	return false
}

func (a *analyzer) checkFieldUniqueness() {
	check := func(name string, members []ast.TypeMember) {
		seen := map[string]lexer.Position{}
		for _, f := range ast.Fields(members) {
			// `_` normalises to "" and `_2` to "2", neither a usable Go field name.
			if gn := idents.GoFieldName(f.Name); gn == "" || !isLetterStart(gn) {
				a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeInvalidGoName,
					"field %q in %q maps to an invalid Go field name %q - a field name must contain a letter and not normalise to a digit-leading or empty identifier. Rename it (e.g. add a leading letter).",
					f.Name, name, gn)
			}
			if prev, exists := seen[f.Name]; exists {
				d := a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeDuplicateField,
					"duplicate field %q in %q", f.Name, name)
				d.Related = related(prev, "first declared here")
			} else {
				seen[f.Name] = f.Pos
			}
		}
	}
	for _, td := range a.pkg.Types {
		check(td.Name, td.Body)
	}
	for _, ed := range a.pkg.Errors {
		check(ed.Name, ed.Body)
		a.checkErrorReservedFieldNames(ed)
	}
}

// errorReservedGoNames are the methods generated on an error type; each
// shadows an embedded body field of the same Go name.
var errorReservedGoNames = map[string]bool{
	"Error": true, "ErrCode": true, "HTTPStatus": true, "WriteResponseHeaders": true,
}

// checkErrorReservedFieldNames rejects an error body field whose Go field name
// collides with a generated error method.
func (a *analyzer) checkErrorReservedFieldNames(ed *ast.ErrorDecl) {
	for _, f := range ast.Fields(ed.Body) {
		if gn := idents.GoFieldName(f.Name); errorReservedGoNames[gn] {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeInvalidGoName,
				"error %s field %q maps to the Go name %q, which collides with the generated error method %s() - the value would be shadowed by the method and produce non-compiling Go. Rename the field.",
				ed.Name, f.Name, gn, gn)
		}
	}
}
