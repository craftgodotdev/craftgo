package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// GenerateEnums writes enums.go under outDir/<pkg.Name>/ with a Go
// type alias and const block per enum. No-op when pkg has no enums.
//
// The Go base type is `int` for int-valued enums, `string` otherwise.
// Constants are named `<EnumName><PascalCase(ValueName)>`; bare values
// use the value name as the string payload.
func GenerateEnums(pkg *semantic.Package, outDir string) error {
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

// enumsView is the template input for enums.tmpl. Decls are sorted
// alphabetically so output stays diff-stable across runs.
type enumsView struct {
	Package string
	Enums   []enumView
}

type enumView struct {
	Name   string
	GoBase string
	Values []enumValueView
}

// enumValueView holds one const row. EnumName is repeated per-value
// so the template can flat-range without `$.` parent refs.
type enumValueView struct {
	ConstName string
	EnumName  string
	Literal   string
}

// buildEnumsView walks pkg.Enums in sorted order. Const-name
// collisions (e.g. `created` and `Created` both mapping to
// `<Enum>Created`) get `_2`, `_3`, ... suffixes via
// [idents.DedupGoFieldNames]; the semantic phase emits a warning
// pointing at the duplicate spellings.
func buildEnumsView(pkg *semantic.Package) enumsView {
	names := sortedKeys(pkg.Enums)
	view := enumsView{Package: pkg.Name, Enums: make([]enumView, 0, len(names))}
	for _, name := range names {
		view.Enums = append(view.Enums, buildEnumView(pkg.Enums[name]))
	}
	return view
}

// buildEnumView shapes one EnumDecl for the template. Semantic has
// already enforced that all values share a kind, so the first
// value's kind decides the Go base type.
func buildEnumView(ed *ast.EnumDecl) enumView {
	goBase := "string"
	if firstEnumKind(ed) == ast.EnumInt {
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

// enumLiteral renders one value's right-hand side. Bare values fall
// back to the source name as the string payload.
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

// firstEnumKind returns the kind of the first value. Empty enums
// fall back to EnumString so the rendered Go file still compiles
// even if a parser regression leaves the values slice empty.
func firstEnumKind(ed *ast.EnumDecl) ast.EnumValueKind {
	values := ed.EnumValues()
	if len(values) == 0 {
		return ast.EnumString
	}
	return values[0].Kind
}

// enumMember is the fully-resolved projection of one enum member, computed
// ONCE per enum so every stage reads the same answer instead of
// re-switching on the kind / re-deriving the const name. ConstName applies
// the case-collision dedup a single time - the const declaration, the
// validate case-list, and the transport default const must all use it (a
// case-list built from a non-deduped name produced `case X, X` and failed
// to compile for a case-colliding enum).
type enumMember struct {
	DSLName    string
	ConstName  string // ed.Name + DedupGoFieldNames(...)[i]
	Kind       ast.EnumValueKind
	Wire       any    // typed wire value: int64 | string
	WireString string // JSON-key / propertyNames form (int -> decimal string)
	Literal    string // Go const right-hand side
}

// enumMembers resolves ed's members in source order, deduping the Go const
// names once. Every const-name and wire-value consumer reads this.
func enumMembers(ed *ast.EnumDecl) []enumMember {
	vals := ed.EnumValues()
	dslNames := make([]string, len(vals))
	for i, v := range vals {
		dslNames[i] = v.Name
	}
	resolved, _ := idents.DedupGoFieldNames(dslNames)
	out := make([]enumMember, len(vals))
	for i, v := range vals {
		out[i] = enumMember{
			DSLName:    v.Name,
			ConstName:  ed.Name + resolved[i],
			Kind:       v.Kind,
			Wire:       enumMemberWire(v),
			WireString: enumMemberWireString(v),
			Literal:    enumLiteral(v),
		}
	}
	return out
}

// enumMemberWire returns a member's typed wire value (int64 / string).
func enumMemberWire(v *ast.EnumValue) any {
	switch v.Kind {
	case ast.EnumInt:
		return v.IntValue
	case ast.EnumString:
		return v.StrValue
	default:
		return v.Name
	}
}

// enumMemberWireString returns a member's wire value as it appears as a
// JSON object key / propertyNames entry - an int-backed member stringifies
// to its decimal form because JSON keys are strings.
func enumMemberWireString(v *ast.EnumValue) string {
	switch v.Kind {
	case ast.EnumInt:
		return strconv.FormatInt(v.IntValue, 10)
	case ast.EnumString:
		return v.StrValue
	default:
		return v.Name
	}
}
