package semantic

import (
	"testing"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// TestDeclNameCaseWarnsForLowercase pins the warning shape for every
// top-level decl kind that codegen treats as a Go identifier verbatim.
// Each lower-case name produces exactly one warning carrying the kind
// keyword the user typed - so the editor squiggle / CI message points
// at the spelling fix, not a generic "decl" label.
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

// TestDeclNameCasePascalCasePasses pins the negative case: every decl
// kind written in canonical PascalCase must NOT raise the warning.
func TestDeclNameCasePascalCasePasses(t *testing.T) {
	expectClean(t, `package x
type MyType { id string }
error NotFound BadName { code string }
enum Priority { Low High }
scalar ID string
service UserService { get ListUsers /u {} }`)
}

// TestDeclNameCaseExtendDoesNotDoubleReport confirms that an extend
// service with a lower-case target name does NOT re-warn on the
// service identifier (the original decl already carries it). Two
// warnings expected total: the primary `service userService` and the
// new `listMore` method added in the extend block. The extend's
// mention of `userService` must not produce a third.
func TestDeclNameCaseExtendDoesNotDoubleReport(t *testing.T) {
	expectCodeCount(t, `package x
service userService {}
extend service userService {
    get listMore /m {}
}`, CodeDeclNameCase, 2)
}

// TestDeclNameCaseEmptyNameSkipped asserts the empty-name guard via
// a direct call to the helper - bypassing parser fixtures that would
// surface parse errors before the analyser ran. An empty name must
// simply return without emitting a diagnostic so parse-error
// recovery does not double-stack with a misleading case warning.
func TestDeclNameCaseEmptyNameSkipped(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.warnNameCase("type", "", lexer.Position{})
	for _, d := range a.diags {
		if d.Code == CodeDeclNameCase {
			t.Errorf("empty-name decl must not produce a name-case warning; got %q", d.Msg)
		}
	}
}
