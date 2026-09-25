package golang

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// generateEnums writes outDir/<pkg>/enums.go, a defined type and const block per
// enum; a package without enums writes nothing.
func generateEnums(pkg *semantic.Package, outDir string) error {
	if len(pkg.Enums) == 0 {
		return nil
	}
	return writeGo(filepath.Join(outDir, pkg.Name, "enums.go"), tmpl("enums.tmpl"), buildEnumsView(pkg))
}

// enumsView is the template input for enums.tmpl.
type enumsView struct {
	Package string
	Enums   []enumView
}

type enumView struct {
	Doc    string
	Name   string
	GoBase string
	Values []enumValueView
}

// enumValueView is one const of an enum.
type enumValueView struct {
	Doc       string
	ConstName string
	EnumName  string
	Literal   string
}

// buildEnumsView returns the enums.tmpl input for pkg, enums in name order.
func buildEnumsView(pkg *semantic.Package) enumsView {
	names := slices.Sorted(maps.Keys(pkg.Enums))
	view := enumsView{Package: pkg.Name, Enums: make([]enumView, 0, len(names))}
	for _, name := range names {
		view.Enums = append(view.Enums, buildEnumView(pkg.Enums[name]))
	}
	return view
}

// buildEnumView returns ed's template view; the Go base is int for an int enum,
// else string.
func buildEnumView(ed *ast.EnumDecl) enumView {
	goBase := "string"
	if semantic.EnumKind(ed) == ast.EnumInt {
		goBase = "int"
	}
	members := enumMembers(ed)
	values := make([]enumValueView, len(members))
	for i, m := range members {
		values[i] = enumValueView{
			Doc:       renderDoc(m.Doc, "\t"),
			ConstName: m.ConstName,
			EnumName:  ed.Name,
			Literal:   m.Literal,
		}
	}
	doc := renderDoc(semantic.DescriptionLines(ed.Decorators, ed.Doc), "")
	return enumView{Doc: doc, Name: ed.Name, GoBase: goBase, Values: values}
}

// enumLiteral renders one value's right-hand side; a bare value is its name as a string.
func enumLiteral(v *ast.EnumValue) string {
	switch v.Kind {
	case ast.EnumInt:
		return strconv.FormatInt(v.IntValue, 10)
	case ast.EnumString:
		return strconv.Quote(v.StrValue)
	default:
		return strconv.Quote(v.Name)
	}
}

// enumMember is one enum member as the Go target renders it.
type enumMember struct {
	DSLName   string
	ConstName string // enum name + member name, deduped across the enum (`EActive_2`)
	Literal   string // Go const right-hand side
	Wire      string // the value on the wire, an int in decimal
	Doc       []string
}

// enumMembers returns ed's members in source order.
func enumMembers(ed *ast.EnumDecl) []enumMember {
	vals := ed.EnumValues()
	dslNames := make([]string, len(vals))
	for i, v := range vals {
		dslNames[i] = v.Name
	}
	consts := idents.EnumConstNames(ed.Name, dslNames)
	out := make([]enumMember, len(vals))
	for i, v := range vals {
		out[i] = enumMember{
			DSLName:   v.Name,
			ConstName: consts[i],
			Literal:   enumLiteral(v),
			Wire:      semantic.EnumMemberWireString(v),
			Doc:       semantic.DescriptionLines(v.Decorators, v.Doc),
		}
	}
	return out
}
