package golang

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/semantic"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// fillData is what fill.tmpl renders: a FillEmpty method per struct of one
// package whose value can hold a required list or map, and the helpers they
// call.
type fillData struct {
	Package    string
	Types      []fillType
	EmptySlice bool
	EmptyMap   bool
	// Maps says a map is copied, which imports maps.
	Maps bool
}

// fillType is one struct's FillEmpty: its name, type parameters and body.
type fillType struct {
	Name       string
	TypeParams []string
	Stmts      []string
}

// fillSet holds the structs of a project - its types and its errors' body
// structs - whose FillEmpty has work: a required list, map or `bytes` of
// their own, a type-parameter value, or a struct below them that has.
type fillSet struct {
	proj  *semantic.Project
	types map[*ast.TypeDecl]bool
	errs  map[*ast.ErrorDecl]bool
}

// fillStruct is one struct the fill set weighs: its package, members and
// type parameters.
type fillStruct struct {
	home       string
	body       []ast.TypeMember
	typeParams []string
}

// newFillSet weighs every struct of proj: one with work of its own, then, to
// a fixed point, one reaching such a struct.
func newFillSet(proj *semantic.Project) *fillSet {
	s := &fillSet{proj: proj, types: map[*ast.TypeDecl]bool{}, errs: map[*ast.ErrorDecl]bool{}}
	structs := map[any]fillStruct{}
	for _, name := range proj.PackageNames() {
		pkg := proj.Packages[name]
		for _, td := range pkg.Types {
			structs[td] = fillStruct{home: name, body: td.Body, typeParams: td.TypeParams}
		}
		for _, ed := range pkg.Errors {
			if len(ast.Members(ed.Body)) > 0 {
				structs[ed] = fillStruct{home: name, body: ast.Members(ed.Body)}
			}
		}
	}
	edges := map[any][]*ast.TypeDecl{}
	for key, st := range structs {
		direct, reached := s.weigh(st)
		edges[key] = reached
		if direct {
			s.mark(key)
		}
	}
	for changed := true; changed; {
		changed = false
		for key, reached := range edges {
			if !s.has(key) && slices.ContainsFunc(reached, func(td *ast.TypeDecl) bool { return s.types[td] }) {
				s.mark(key)
				changed = true
			}
		}
	}
	return s
}

// mark records the struct key, a type or an error, as having work.
func (s *fillSet) mark(key any) {
	switch d := key.(type) {
	case *ast.TypeDecl:
		s.types[d] = true
	case *ast.ErrorDecl:
		s.errs[d] = true
	}
}

// has reports whether the struct key has work.
func (s *fillSet) has(key any) bool {
	switch d := key.(type) {
	case *ast.TypeDecl:
		return s.types[d]
	case *ast.ErrorDecl:
		return s.errs[d]
	}
	return false
}

// weigh reports whether st has work of its own, and returns the struct types
// its fields and mixins name, walking st as the emitter does with no struct
// counted as having work yet.
func (s *fillSet) weigh(st fillStruct) (direct bool, reached []*ast.TypeDecl) {
	e := &fillEmitter{
		res:        semantic.NewResolver(s.proj, st.home),
		pkg:        s.proj.Packages[st.home],
		set:        &fillSet{proj: s.proj, types: map[*ast.TypeDecl]bool{}, errs: map[*ast.ErrorDecl]bool{}},
		data:       &fillData{},
		typeParams: st.typeParams,
		reached:    &reached,
	}
	return len(e.body(st.body)) > 0, reached
}

// fillsBytes reports whether a value of type rf is a byte slice that encodes
// as a JSON string: `bytes`, or a scalar over it, raw bytes aside.
func fillsBytes(rf semantic.ResolvedField) bool {
	return rf.ResolvedPrim == "bytes" && (rf.Category == semantic.CatBytes || rf.Category == semantic.CatScalar)
}

// pkgFills reports whether pkg declares a struct with work, so it gets a fill.go.
func (s *fillSet) pkgFills(pkg *semantic.Package) bool {
	for _, td := range pkg.Types {
		if s.types[td] {
			return true
		}
	}
	for _, ed := range pkg.Errors {
		if s.errs[ed] {
			return true
		}
	}
	return false
}

// generateFill writes outDir/<pkg>/fill.go when pkg declares a struct with work.
func generateFill(pkg *semantic.Package, outDir string, fills *fillSet) error {
	if !fills.pkgFills(pkg) {
		return nil
	}
	return writeGo(filepath.Join(outDir, pkg.Name, "fill.go"), tmpl("fill.tmpl"), fills.data(pkg))
}

// data builds pkg's fill.go: its types, then its errors' body structs.
func (s *fillSet) data(pkg *semantic.Package) fillData {
	out := fillData{Package: pkg.Name}
	e := &fillEmitter{res: semantic.NewResolver(s.proj, pkg.Name), pkg: pkg, set: s, data: &out}
	for _, name := range slices.Sorted(maps.Keys(pkg.Types)) {
		if td := pkg.Types[name]; s.types[td] {
			e.typeParams = td.TypeParams
			out.Types = append(out.Types, fillType{Name: name, TypeParams: td.TypeParams, Stmts: e.body(td.Body)})
		}
	}
	e.typeParams = nil
	for _, name := range slices.Sorted(maps.Keys(pkg.Errors)) {
		if ed := pkg.Errors[name]; s.errs[ed] {
			out.Types = append(out.Types, fillType{Name: idents.ErrorBodyName(ed.Name), Stmts: e.body(ast.Members(ed.Body))})
		}
	}
	return out
}

// fillEmitter renders the FillEmpty statements of one package's structs;
// reached, when set, collects every struct type a field or mixin names.
type fillEmitter struct {
	res        *semantic.Resolver
	pkg        *semantic.Package
	set        *fillSet
	data       *fillData
	typeParams []string
	reached    *[]*ast.TypeDecl
}

// structType returns the struct type n names, noting it in reached, or nil.
func (e *fillEmitter) structType(n *ast.NamedTypeRef) *ast.TypeDecl {
	td := e.res.LookupType(n.Name.String())
	if td != nil && e.reached != nil {
		*e.reached = append(*e.reached, td)
	}
	return td
}

// fillCall is the method a probe finds on a type-parameter value.
const fillCall = "interface{ FillEmpty(int) (bool, bool) }"

// body renders the statements filling the fields and mixins of a struct held
// in v; a value v holds itself sets `changed`.
func (e *fillEmitter) body(members []ast.TypeMember) []string {
	var out []string
	names := resolvedGoFieldNames(members)
	i := 0
	for _, m := range members {
		switch v := m.(type) {
		case *ast.Field:
			access := "v." + names[i]
			i++
			_, presence := wire.JSONShape(v)
			if presence == wire.JSONAbsent {
				continue
			}
			rf := semantic.ResolveField(v, e.pkg, e.res.Project())
			required := presence == wire.JSONRequired && !semantic.HasRawFormat(v.Decorators)
			stmts, _ := e.value(v.Type, access, required, rf.GoPointer() && rf.Category != semantic.CatFile, 0, "changed")
			out = append(out, stmts...)
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil || len(v.Ref.Name.Parts) == 0 {
				continue
			}
			if td := e.structType(v.Ref); td != nil && e.set.types[td] {
				out = append(out, fillInto("v."+v.Ref.Name.Parts[len(v.Ref.Name.Parts)-1]+".FillEmpty(depth + 1)", "changed"))
			}
		}
	}
	return out
}

// value renders the statements filling a value of type t held in access: a
// nil list or map set empty when required, then what each element, map value
// or struct below holds. A change to the value access holds itself sets
// flag, "" when none matters: an element or a pointer's target is shared, so
// a copy holding it needs no write-back. A map is never written: a copy
// holding the changed values takes its place. ptr says the Go value is a
// pointer; lvl keeps the variables of nested loops apart. It also reports
// whether a statement sets flag.
func (e *fillEmitter) value(t *ast.TypeRef, access string, required, ptr bool, lvl int, flag string) ([]string, bool) {
	var out []string
	sets := false
	switch {
	case t == nil:
	case t.Array:
		if required {
			out = append(out, setFlag("emptySlice(&"+access+")", flag))
			sets = flag != ""
			e.data.EmptySlice = true
		}
		i, elem := fmt.Sprintf("i%d", lvl), t.ElemTypeRef()
		if inner, _ := e.value(elem, access+"["+i+"]", !elem.Optional, e.nested(elem), lvl+1, ""); len(inner) > 0 {
			out = append(out, fmt.Sprintf("for %s := range %s {\n%s\n}", i, access, strings.Join(inner, "\n")))
		}
	case t.Map != nil:
		if required {
			out = append(out, setFlag("emptyMap(&"+access+")", flag))
			sets = flag != ""
			e.data.EmptyMap = true
		}
		n := lvl
		inner, innerSets := e.value(t.Map.Value, fmt.Sprintf("e%d", n), !t.Map.Value.Optional, e.nested(t.Map.Value), lvl+1, fmt.Sprintf("c%d", n))
		switch {
		case len(inner) == 0:
		case !innerSets:
			out = append(out, fmt.Sprintf("for _, e%d := range %s {\n%s\n}", n, access, strings.Join(inner, "\n")))
		default:
			e.data.Maps = true
			done := fmt.Sprintf("%s = m%d", access, n)
			if flag != "" {
				done += "\n" + flag + " = true"
				sets = true
			}
			out = append(out, fmt.Sprintf(`{
m%[1]d, cloned%[1]d := %[2]s, false
for k%[1]d, e%[1]d := range %[2]s {
c%[1]d := false
%[3]s
if c%[1]d {
if !cloned%[1]d {
m%[1]d, cloned%[1]d = maps.Clone(%[2]s), true
}
m%[1]d[k%[1]d] = e%[1]d
}
}
if cloned%[1]d {
%[4]s
}
}`, n, access, strings.Join(inner, "\n"), done))
		}
	case t.Named == nil || t.Named.Name == nil:
	case slices.Contains(e.typeParams, t.Named.Name.String()):
		if ptr {
			out = append(out, guardBlock(access, fmt.Sprintf("if f, ok := any(%s).(%s); ok {\n%s\n}", access, fillCall, fillInto("f.FillEmpty(depth + 1)", ""))))
		} else {
			out = append(out, fmt.Sprintf("if f, ok := any(&%s).(%s); ok {\n%s\n}", access, fillCall, fillInto("f.FillEmpty(depth + 1)", flag)))
			sets = flag != ""
		}
	default:
		if td := e.structType(t.Named); td != nil {
			if !e.set.types[td] {
				return nil, false
			}
			if ptr {
				out = append(out, guardBlock(access, fillInto(access+".FillEmpty(depth + 1)", "")))
			} else {
				out = append(out, fillInto(access+".FillEmpty(depth + 1)", flag))
				sets = flag != ""
			}
		} else if required && fillsBytes(e.res.ResolveTypeRef(t)) {
			out = append(out, setFlag("emptySlice(&"+access+")", flag))
			sets = flag != ""
			e.data.EmptySlice = true
		}
	}
	return out, sets
}

// setFlag renders call, a helper reporting whether it set a value, setting
// flag when it did; "" drops the report.
func setFlag(call, flag string) string {
	if flag == "" {
		return call
	}
	return fmt.Sprintf("if %s {\n%s = true\n}", call, flag)
}

// fillInto renders call, a FillEmpty call, returning at once when it stopped
// and setting flag when it changed the value; "" drops the change.
func fillInto(call, flag string) string {
	if flag == "" {
		return fmt.Sprintf("if _, s := %s; s {\nreturn changed, true\n}", call)
	}
	return fmt.Sprintf("if c, s := %s; s {\nreturn changed, true\n} else if c {\n%s = true\n}", call, flag)
}

// nested reports whether an element or map value of type t is a pointer in
// Go: an optional value whose type holds no nil itself.
func (e *fillEmitter) nested(t *ast.TypeRef) bool {
	if !t.Optional || t.Array || t.Map != nil {
		return false
	}
	if t.Named != nil && t.Named.Name != nil && slices.Contains(e.typeParams, t.Named.Name.String()) {
		return true
	}
	return !e.res.ResolveTypeRef(t).IsNilable
}
