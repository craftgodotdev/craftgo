package golang

import (
	"fmt"
	"maps"
	"os"
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
	if pkg.Name == "" {
		return fmt.Errorf("package has no name")
	}
	if len(pkg.Enums) == 0 {
		return nil
	}
	pkgDir := filepath.Join(outDir, pkg.Name)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	formatted, err := renderGo(tmpl("enums.tmpl"), buildEnumsView(pkg))
	if err != nil {
		return fmt.Errorf("render enums.go: %w", err)
	}
	return os.WriteFile(filepath.Join(pkgDir, "enums.go"), formatted, 0o644)
}

// enumsView is the template input for enums.tmpl.
type enumsView struct {
	Package string
	Enums   []enumView
}

type enumView struct {
	Name   string
	GoBase string
	Values []enumValueView
}

// enumValueView is one const of an enum.
type enumValueView struct {
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
			ConstName: m.ConstName,
			EnumName:  ed.Name,
			Literal:   m.Literal,
		}
	}
	return enumView{Name: ed.Name, GoBase: goBase, Values: values}
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
	DSLName    string
	ConstName  string // enum name + member name, deduped across the enum (`EActive_2`)
	Kind       ast.EnumValueKind
	Wire       any    // typed wire value: int64 | string
	WireString string // JSON-key / propertyNames form (int -> decimal string)
	Literal    string // Go const right-hand side
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
			DSLName:    v.Name,
			ConstName:  consts[i],
			Kind:       v.Kind,
			Wire:       semantic.EnumMemberWire(v),
			WireString: semantic.EnumMemberWireString(v),
			Literal:    enumLiteral(v),
		}
	}
	return out
}
