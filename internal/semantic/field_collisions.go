package semantic

import (
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// checkFieldNameCollisions emits a warning for each type / error body
// in which two or more DSL field names normalise to the same Go
// identifier under [idents.GoFieldName]. The canonical example:
//
//	type User {
//	    user_id string  // → Go field UserID
//	    userId  string  // → Go field UserID - same name!
//	}
//
// Without intervention, codegen would emit two `UserID` fields and
// the resulting Go file would not compile. The codegen pass in
// [internal/codegen] disambiguates by appending `_2`, `_3`, ...
// suffixes - but that's a silent fix that hides a schema-level
// duplication: BOTH fields keep their DSL spelling on the JSON wire,
// so the API contract carries two distinct keys for what the user
// almost certainly meant as one.
//
// Surfaced as a warning (not an error) so existing projects with
// intentional aliasing keep building; the squiggle in the IDE is
// loud enough to flag accidental cases without breaking CI.
func (a *analyzer) checkFieldNameCollisions(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			a.checkOneDeclFieldCollisions(d)
		}
	}
}

// checkOneDeclFieldCollisions inspects a single TypeDecl / ErrorDecl
// body. Other decl shapes have no field collisions to warn about.
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

// warnFieldCollisions runs the dedup over the body's field names and
// emits one diagnostic per collision group, anchored at the first
// duplicate's position so the IDE highlights the offending field
// rather than the (innocent) original.
func (a *analyzer) warnFieldCollisions(parent string, members []ast.TypeMember) {
	var fields []*ast.Field
	for _, m := range members {
		if f, ok := m.(*ast.Field); ok && f.Name != "" {
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
	// Index by DSL spelling → field for fast lookup of a position
	// to anchor each diagnostic at.
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
		// Emit one diagnostic per DUPLICATE - anchor at each dupe's
		// own position so all colliding fields get a squiggle in the
		// editor, not just the first one in the group. The message
		// names the canonical "winner" so the user sees which DSL
		// spelling keeps the bare Go name vs. which receives the
		// `_N` suffix.
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

// isLetterStart reports whether s begins with a Unicode letter - the
// requirement for a valid (exported) Go identifier start.
func isLetterStart(s string) bool {
	for _, r := range s {
		return unicode.IsLetter(r)
	}
	return false
}

func (a *analyzer) checkFieldUniqueness() {
	check := func(name string, members []ast.TypeMember) {
		seen := map[string]lexer.Position{}
		for _, m := range members {
			f, ok := m.(*ast.Field)
			if !ok {
				continue
			}
			// The field's Go identifier must be a non-empty, letter-leading
			// exported name. `_` / `__` normalise to "" (an anonymous embed)
			// and `_2` normalises to the digit-leading "2" - both make the
			// struct emitter produce uncompilable or unexported Go with no
			// diagnostic. Reject here so the author gets a clean message.
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

// errorReservedGoNames are the methods the error template generates on the
// error type. A body field whose Go name matches one is shadowed by the
// method (Go selector resolution binds the shallower method over the embedded
// field), so `e.<Name>` is a func value - the response-header writer then
// emits non-compiling Go, and even a plain body field can't be reached by
// that selector.
var errorReservedGoNames = map[string]bool{
	"Error": true, "ErrCode": true, "HTTPStatus": true, "WriteResponseHeaders": true,
}

// checkErrorReservedFieldNames rejects an error body field whose Go field name
// collides with a generated error method.
func (a *analyzer) checkErrorReservedFieldNames(ed *ast.ErrorDecl) {
	for _, m := range ed.Body {
		f, ok := m.(*ast.Field)
		if !ok {
			continue
		}
		if gn := idents.GoFieldName(f.Name); errorReservedGoNames[gn] {
			a.diag(f.Pos, f.Pos, lexer.SeverityError, CodeInvalidGoName,
				"error %s field %q maps to the Go name %q, which collides with the generated error method %s() - the value would be shadowed by the method and produce non-compiling Go. Rename the field.",
				ed.Name, f.Name, gn, gn)
		}
	}
}
