package golang

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// assignedFields returns the c.<path> fields the rendered applyDefaults assigns, in order.
func assignedFields(t *testing.T, src []byte) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "config.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyDefaults" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok {
				for _, lhs := range as.Lhs {
					got = append(got, strings.TrimPrefix(exprText(lhs), "c."))
				}
			}
			return true
		})
	}
	return got
}

// exprText spells a selector chain such as c.Server.Addr.
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprText(x.X) + "." + x.Sel.Name
	}
	return "?"
}

// The config scaffold defaults only what the runtime has no default for: the listener
// addresses and the service name.
func TestConfigScaffoldDefaultsOnlyWhatRuntimeLacks(t *testing.T) {
	for name, tc := range map[string]struct {
		http, grpc bool
		want       []string
	}{
		"http":  {true, false, []string{"Server.Addr", "ServiceName"}},
		"grpc":  {false, true, []string{"GRPC.Addr", "ServiceName"}},
		"mixed": {true, true, []string{"Server.Addr", "GRPC.Addr", "ServiceName"}},
	} {
		data := runtimeData{OperationName: "app", ConfigImport: "example.com/app/config", ConfigDir: "config", HasHTTP: tc.http, HasGRPC: tc.grpc}
		src, err := renderScaffold(tmpl("config.go.tmpl"), data)
		if err != nil {
			t.Fatal(err)
		}
		if got := assignedFields(t, src); !slices.Equal(got, tc.want) {
			t.Errorf("%s: applyDefaults assigns %v, want %v", name, got, tc.want)
		}
	}
}
