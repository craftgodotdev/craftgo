package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// A lower-case declaration name warns, naming the declaration's kind.
func TestDeclNameCaseWarnsForLowercase(t *testing.T) {
	cases := []struct {
		label  string
		src    string
		wantIn string // substring expected in the warning text
	}{
		{"type", `package x
type myType { id string }`, `type name "myType"`},
		{"error", `package x
error NotFound badName { code string }`, `error name "badName"`},
		{"enum", `package x
enum priority { low high }`, `enum name "priority"`},
		{"service", `package x
service userService { }`, `service name "userService"`},
		{"scalar", `package x
scalar id string`, `scalar name "id"`},
		{"method", `package x
service S { get listUsers /u {} }`, `method name "listUsers"`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			d := expectWarning(t, c.src, CodeDeclNameCase)
			expectMessage(t, d, c.wantIn)
		})
	}
}

// PascalCase declaration names do not warn.
func TestDeclNameCasePascalCasePasses(t *testing.T) {
	expectClean(t, `package x
type MyType { id string }
error NotFound BadName { code string }
enum Priority { Low High }
scalar ID string
service UserService { get ListUsers /u {} }`)
}

// An `extend service` does not warn again about its lower-case service name.
func TestDeclNameCaseExtendDoesNotDoubleReport(t *testing.T) {
	expectCodeCount(t, `package x
service userService {}
extend service userService {
    get listMore /m {}
}`, CodeDeclNameCase, 2)
}

// warnNameCase skips an empty name.
func TestDeclNameCaseEmptyNameSkipped(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.warnNameCase("type", "", lexer.Position{})
	for _, d := range a.diags {
		if d.Code == CodeDeclNameCase {
			t.Errorf("empty-name decl must not produce a name-case warning; got %q", d.Msg)
		}
	}
}
