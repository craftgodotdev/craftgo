package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// A method's chain holds the primary service's decorators, then its extend
// block's, then its own; only the method's own @ignoreMiddleware drops the
// inherited ones.
func TestInheritedDecorators(t *testing.T) {
	pkg := expectClean(t, `package app
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
		"Four":  "A |  | false",
	}
	for _, m := range svc.Methods {
		service, member, ignored := svc.InheritedDecorators(m, "middlewares")
		got := names(service) + " | " + names(member) + " | " + map[bool]string{true: "true", false: "false"}[ignored]
		if got != want[m.Name] {
			t.Errorf("%s: chain = %q, want %q", m.Name, got, want[m.Name])
		}
	}
}
