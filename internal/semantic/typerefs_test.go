package semantic

import (
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// walkTypeRefs visits every type position of each declaration kind, a
// reference's arguments before the reference, and flags only a mixin's name.
func TestWalkTypeRefsVisitsEveryPosition(t *testing.T) {
	files := parseFiles(t, `package app
type Box<T> {
    v T
    m map<K, V[]>
    Page<Arg>
}
error NotFound Gone { f ErrField }
event Created { payload Payload<PArg> }
service S {
    get M /m {
        request  Req
        response Resp<RArg>
    }
}
scalar Email string
enum Color { Red }
middleware Auth`)
	var got []string
	for _, d := range files[0].Decls {
		walkTypeRefs(d, func(n *ast.NamedTypeRef, typeParams []string, mixin bool) {
			s := n.Name.String()
			if mixin {
				s += " mixin"
			}
			if len(typeParams) > 0 {
				s += " in <" + strings.Join(typeParams, ",") + ">"
			}
			got = append(got, s)
		})
	}
	want := []string{
		"T in <T>", "K in <T>", "V in <T>", "Arg in <T>", "Page mixin in <T>",
		"ErrField",
		"PArg", "Payload",
		"Req", "RArg", "Resp",
	}
	if !slices.Equal(got, want) {
		t.Errorf("visited\n got %v\nwant %v", got, want)
	}
}

// A reference without a name, as half-typed source leaves one, is skipped.
func TestCheckTypeRefNameless(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.checkTypeRef(&ast.NamedTypeRef{}, nil, nil, false)
	a.checkTypeRef(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{}}, nil, nil, false)
	if len(a.diags) != 0 {
		t.Errorf("nameless ref should not diag, got %v", a.diags)
	}
}

// An import with an empty path gets no path diagnostic.
func TestCheckImportPathEmpty(t *testing.T) {
	a := newTestAnalyzer(&Package{Name: "app"})
	a.checkImportPath(&ast.Import{Path: ""})
	if len(a.diags) != 0 {
		t.Errorf("empty import path should not diag, got %v", a.diags)
	}
}
