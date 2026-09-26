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
// its fields and mixins name.
func (s *fillSet) weigh(st fillStruct) (direct bool, reached []*ast.TypeDecl) {
	res := semantic.NewResolver(s.proj, st.home)
	var visit func(t *ast.TypeRef, required bool)
	visit = func(t *ast.TypeRef, required bool) {
		switch {
		case t == nil:
		case t.Array:
			direct = direct || required
			elem := t.ElemTypeRef()
			visit(elem, !elem.Optional)
		case t.Map != nil:
			direct = direct || required
			visit(t.Map.Value, !t.Map.Value.Optional)
		case t.Named == nil || t.Named.Name == nil:
		case slices.Contains(st.typeParams, t.Named.Name.String()):
			direct = true
		default:
			if td := res.LookupType(t.Named.Name.String()); td != nil {
				reached = append(reached, td)
			} else if required && fillsBytes(res.ResolveTypeRef(t)) {
				direct = true
			}
		}
	}
	for _, m := range st.body {
		switch v := m.(type) {
		case *ast.Field:
			if _, presence := wire.JSONShape(v); presence != wire.JSONAbsent {
				visit(v.Type, presence == wire.JSONRequired && !semantic.HasRawFormat(v.Decorators))
			}
		case *ast.Mixin:
			if v.Ref != nil && v.Ref.Name != nil {
				if td := res.LookupType(v.Ref.Name.String()); td != nil {
					reached = append(reached, td)
				}
			}
		}
	}
	return direct, reached
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

// fillEmitter renders the FillEmpty statements of one package's structs.
type fillEmitter struct {
	res        *semantic.Resolver
	pkg        *semantic.Package
	set        *fillSet
	data       *fillData
	typeParams []string
}

// body renders the statements filling the fields and mixins of a struct held
// in v.
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
			out = append(out, e.value(v.Type, access, required, rf.GoPointer() && rf.Category != semantic.CatFile, 0)...)
		case *ast.Mixin:
			if v.Ref == nil || v.Ref.Name == nil || len(v.Ref.Name.Parts) == 0 {
				continue
			}
			if td := e.res.LookupType(v.Ref.Name.String()); td != nil && e.set.types[td] {
				out = append(out, fmt.Sprintf("v.%s.FillEmpty(depth + 1)", v.Ref.Name.Parts[len(v.Ref.Name.Parts)-1]))
			}
		}
	}
	return out
}

// value renders the statements filling a value of type t held in the
// addressable access: a nil list or map set empty when required, then what
// each element, map value or struct below holds. ptr says the Go value is a
// pointer; lvl keeps the variables of nested loops apart.
func (e *fillEmitter) value(t *ast.TypeRef, access string, required, ptr bool, lvl int) []string {
	var out []string
	switch {
	case t == nil:
	case t.Array:
		if required {
			out = append(out, "emptySlice(&"+access+")")
			e.data.EmptySlice = true
		}
		i, elem := fmt.Sprintf("i%d", lvl), t.ElemTypeRef()
		if inner := e.value(elem, access+"["+i+"]", !elem.Optional, e.nested(elem), lvl+1); len(inner) > 0 {
			out = append(out, fmt.Sprintf("for %s := range %s {\n%s\n}", i, access, strings.Join(inner, "\n")))
		}
	case t.Map != nil:
		if required {
			out = append(out, "emptyMap(&"+access+")")
			e.data.EmptyMap = true
		}
		k, v := fmt.Sprintf("k%d", lvl), fmt.Sprintf("e%d", lvl)
		if inner := e.value(t.Map.Value, v, !t.Map.Value.Optional, e.nested(t.Map.Value), lvl+1); len(inner) > 0 {
			out = append(out, fmt.Sprintf("for %s, %s := range %s {\n%s\n%s[%s] = %s\n}", k, v, access, strings.Join(inner, "\n"), access, k, v))
		}
	case t.Named == nil || t.Named.Name == nil:
	case slices.Contains(e.typeParams, t.Named.Name.String()):
		probe := "&" + access
		if ptr {
			probe = access
		}
		call := fmt.Sprintf("if f, ok := any(%s).(interface{ FillEmpty(int) }); ok {\nf.FillEmpty(depth + 1)\n}", probe)
		if ptr {
			call = guardBlock(access, call)
		}
		out = append(out, call)
	default:
		if td := e.res.LookupType(t.Named.Name.String()); td != nil {
			if !e.set.types[td] {
				return nil
			}
			call := access + ".FillEmpty(depth + 1)"
			if ptr {
				call = guardBlock(access, call)
			}
			out = append(out, call)
		} else if required && fillsBytes(e.res.ResolveTypeRef(t)) {
			out = append(out, "emptySlice(&"+access+")")
			e.data.EmptySlice = true
		}
	}
	return out
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
