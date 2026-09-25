package semantic

import (
	"slices"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// goName is a Go identifier a declaration emits.
type goName struct {
	name string
	// source names what emits it, such as `error "UserGone"`.
	source string
	// role says what the identifier is to its source, such as "its code constant".
	role string
	pos  lexer.Position
}

// checkDeclGoNameCollisions rejects two declarations of one package that
// emit the same Go identifier into its types package or its events package.
func (a *analyzer) checkDeclGoNameCollisions(files []*ast.File) {
	var types, events []goName
	for _, f := range files {
		for _, d := range f.Decls {
			if d.DeclName() == "" {
				continue
			}
			if ev, ok := d.(*ast.EventDecl); ok {
				events = append(events, eventGoNames(ev)...)
				continue
			}
			types = append(types, typesGoNames(d)...)
		}
	}
	a.reportGoNameCollisions(types)
	a.reportGoNameCollisions(events)
}

// reportGoNameCollisions reports each identifier of one Go package that
// names emits more than once, every later one by position against the first.
func (a *analyzer) reportGoNameCollisions(names []goName) {
	groups := map[string][]goName{}
	var order []string
	for _, n := range names {
		if _, seen := groups[n.name]; !seen {
			order = append(order, n.name)
		}
		groups[n.name] = append(groups[n.name], n)
	}
	for _, name := range order {
		ns := groups[name]
		if len(ns) < 2 {
			continue
		}
		slices.SortStableFunc(ns, func(x, y goName) int { return comparePos(x.pos, y.pos) })
		first := ns[0]
		for _, dupe := range ns[1:] {
			d := a.diag(dupe.pos, dupe.pos, lexer.SeverityError, CodeDeclGoNameCollision,
				"%s would emit Go identifier %q as %s, which %s already emits as %s - codegen cannot disambiguate decl names; rename one to fix",
				dupe.source, name, dupe.role, first.source, first.role)
			d.Related = related(first.pos, "first emitted here")
		}
	}
}

// typesGoNames returns the Go identifiers d emits into its package's types
// package; a middleware's live in the svccontext package.
func typesGoNames(d ast.Decl) []goName {
	switch dd := d.(type) {
	case *ast.TypeDecl:
		return []goName{{name: dd.Name, source: `type "` + dd.Name + `"`, role: "its type", pos: dd.Pos}}
	case *ast.ScalarDecl:
		return []goName{{name: dd.Name, source: `scalar "` + dd.Name + `"`, role: "its type", pos: dd.Pos}}
	case *ast.EnumDecl:
		out := []goName{{name: dd.Name, source: `enum "` + dd.Name + `"`, role: "its type", pos: dd.Pos}}
		values := dd.EnumValues()
		names := make([]string, len(values))
		for i, v := range values {
			names[i] = v.Name
		}
		for i, c := range idents.EnumConstNames(dd.Name, names) {
			if v := values[i]; v.Name != "" {
				out = append(out, goName{name: c, source: `enum value "` + dd.Name + "." + v.Name + `"`, role: "its constant", pos: v.Pos})
			}
		}
		return out
	case *ast.ErrorDecl:
		source := `error "` + dd.Name + `"`
		out := []goName{
			{name: idents.ErrorTypeName(dd.Name), source: source, role: "its type", pos: dd.Pos},
			{name: idents.ErrorCodeName(dd.Name), source: source, role: "its code constant", pos: dd.Pos},
			{name: idents.ErrorConstructorName(dd.Name), source: source, role: "its constructor", pos: dd.Pos},
		}
		if len(ast.Members(dd.Body)) > 0 {
			out = append(out, goName{name: idents.ErrorBodyName(dd.Name), source: source, role: "its body struct", pos: dd.Pos})
		}
		return out
	}
	return nil
}

// eventGoNames returns the Go identifiers ev emits into its package's events
// package.
func eventGoNames(ev *ast.EventDecl) []goName {
	source := `event "` + ev.Name + `"`
	return []goName{
		{name: ev.Name, source: source, role: "its descriptor", pos: ev.Pos},
		{name: idents.EventContractName(ev.Name), source: source, role: "its contract constant", pos: ev.Pos},
	}
}

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

// checkJSONKeys rejects two body fields of one type or error, mixin fields
// included, that would share a JSON key.
func (a *analyzer) checkJSONKeys(files []*ast.File) {
	for _, f := range files {
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.TypeDecl:
				a.checkJSONKeysIn("type "+dd.Name, dd.Body)
			case *ast.ErrorDecl:
				a.checkJSONKeysIn("error "+dd.Name, dd.Body)
			}
		}
	}
}

// checkJSONKeysIn reports each field of members that shares a JSON key with
// an earlier one: at the body's own field when one of the pair is, as
// encoding/json keeps only one of them.
func (a *analyzer) checkJSONKeysIn(parent string, members []ast.TypeMember) {
	own := ast.Fields(members)
	fields, _ := a.proj.flattenFields(a.pkg.Name, a.pkg.Name, members, nil, nil)
	seen := map[string]*ast.Field{}
	for _, ff := range fields {
		f := ff.Field
		if f.Name == "" {
			continue
		}
		name, presence := wire.JSONShape(f)
		if presence == wire.JSONAbsent {
			continue
		}
		prev, dup := seen[name]
		if !dup {
			seen[name] = f
			continue
		}
		at, other := f, prev
		if !slices.Contains(own, f) && slices.Contains(own, prev) {
			at, other = prev, f
		}
		d := a.diag(at.Pos, at.Pos, lexer.SeverityError, CodeFieldNameCollision,
			"field %q of %s carries JSON key %q, which field %q also carries - two members cannot share one key", at.Name, parent, name, other.Name)
		d.Related = related(other.Pos, "also carried here")
	}
}

// checkEnumValueCollisions warns when two values of an enum map to one Go
// const name.
func (a *analyzer) checkEnumValueCollisions(files []*ast.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			if ed, ok := d.(*ast.EnumDecl); ok {
				a.warnEnumValueCollisions(ed)
			}
		}
	}
}

// warnEnumValueCollisions reports every colliding value of ed but the first.
func (a *analyzer) warnEnumValueCollisions(ed *ast.EnumDecl) {
	if ed == nil {
		return
	}
	enumVals := ed.EnumValues()
	if len(enumVals) < 2 {
		return
	}
	names := make([]string, 0, len(enumVals))
	for _, v := range enumVals {
		if v == nil || v.Name == "" {
			continue
		}
		names = append(names, v.Name)
	}
	if len(names) < 2 {
		return
	}
	_, collisions := idents.DedupGoFieldNames(names)
	if len(collisions) == 0 {
		return
	}
	byName := map[string]*ast.EnumValue{}
	for _, v := range enumVals {
		if v != nil {
			byName[v.Name] = v
		}
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
			d := a.diag(anchor.Pos, anchor.Pos, lexer.SeverityWarning, CodeEnumValueCollision,
				"enum value %q collides with %q in enum %s - both normalise to Go const %q; codegen will emit %q to keep the package compilable, but the wire payloads stay distinct (rename one if this duplication was unintended)",
				dupeName, firstDSL, ed.Name, ed.Name+c.CanonicalGoName, ed.Name+c.ResolvedGoNames[rank+1])
			if first != nil {
				d.Related = related(first.Pos, "first declared here (keeps the canonical const name)")
			}
		}
	}
}
