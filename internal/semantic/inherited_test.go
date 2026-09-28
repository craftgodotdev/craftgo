package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A method's chain holds the primary service's decorators, then its extend
// block's, then its own. The method's own @ignoreMiddleware drops the
// inherited ones; its block's drops the primary's, as written on the method
// ahead of the block's own @middlewares.
func TestInheritedDecorators(t *testing.T) {
	pkg := mustClean(t, `package app
middleware A
middleware B
middleware C
@middlewares(A)
service S {
    @middlewares(C)
    get One /one {}
}
@middlewares(B)
extend service S {
    @middlewares(C)
    get Two /two {}

    @ignoreMiddleware
    @middlewares(C)
    get Three /three {}
}
@ignoreMiddleware
extend service S {
    get Four /four {}
}
@ignoreMiddleware
@middlewares(B)
extend service S {
    @middlewares(C)
    get Five /five {}
}`)
	svc := pkg.Services["S"]
	names := func(ds []*ast.Decorator) string {
		var out []string
		for _, d := range ds {
			for _, n := range ast.ArgNames(d) {
				out = append(out, n.Value)
			}
		}
		return strings.Join(out, ",")
	}
	want := map[string]string{
		"One":   "A | C | false",
		"Two":   "A | B,C | false",
		"Three": " | C | true",
		"Four":  " |  | true",
		"Five":  " | B,C | true",
	}
	for _, m := range svc.Methods {
		service, member, ignored := svc.InheritedDecorators(m, "middlewares")
		got := names(service) + " | " + names(member) + " | " + map[bool]string{true: "true", false: "false"}[ignored]
		if got != want[m.Name] {
			t.Errorf("%s: chain = %q, want %q", m.Name, got, want[m.Name])
		}
	}
}

// A method is hidden by its own @hidden, its extend block's or its primary
// service's.
func TestServiceInfoHidden(t *testing.T) {
	pkg := mustClean(t, `package app
service S {
    get One /one {}
    @hidden
    get Two /two {}
}
@hidden
extend service S {
    get Three /three {}
}
extend service S {
    get Four /four {}
}
@hidden
service T {
    get Five /five {}
}
@hidden
extend service T {
    get Six /six {}
}`)
	want := map[string]bool{"One": false, "Two": true, "Three": true, "Four": false, "Five": true, "Six": true}
	for _, key := range pkg.ServiceNames() {
		svc := pkg.Services[key]
		for _, m := range svc.Methods {
			if got := svc.Hidden(m); got != want[m.Name] {
				t.Errorf("%s.Hidden() = %v, want %v", m.Name, got, want[m.Name])
			}
		}
	}
}
