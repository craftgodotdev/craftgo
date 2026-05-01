package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// TestDeclNameCaseWarnsForLowercase pins the warning shape for every
// top-level decl kind that codegen treats as a Go identifier verbatim.
// Each lower-case name produces exactly one warning carrying the kind
// keyword the user typed — so the editor squiggle / CI message points
// at the spelling fix, not a generic "decl" label.
func TestDeclNameCaseWarnsForLowercase(t *testing.T) {
	cases := []struct {
		label  string
		src    string
		wantIn string // substring expected in the warning text
	}{
		{
			label:  "type",
			src:    `package x` + "\n" + `type myType { id string }`,
			wantIn: `type name "myType"`,
		},
		{
			label:  "error",
			src:    `package x` + "\n" + `error NotFound badName { code string }`,
			wantIn: `error name "badName"`,
		},
		{
			label:  "enum",
			src:    `package x` + "\n" + `enum priority { low high }`,
			wantIn: `enum name "priority"`,
		},
		{
			label:  "service",
			src:    `package x` + "\n" + `service userService { }`,
			wantIn: `service name "userService"`,
		},
		{
			label:  "scalar",
			src:    `package x` + "\n" + `scalar id string`,
			wantIn: `scalar name "id"`,
		},
		{
			label:  "method",
			src:    `package x` + "\n" + `service S { get listUsers /u {} }`,
			wantIn: `method name "listUsers"`,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			_, diags := Analyze(parseFiles(t, c.src))
			d := findCode(diags, CodeDeclNameCase)
			if d == nil {
				t.Fatalf("expected %s warning, got %v", CodeDeclNameCase, codes(diags))
			}
			if d.Severity != lexer.SeverityWarning {
				t.Errorf("expected warning severity, got %v", d.Severity)
			}
			if !strings.Contains(d.Msg, c.wantIn) {
				t.Errorf("warning text must mention %q (so the user knows which spelling to fix); got %q", c.wantIn, d.Msg)
			}
		})
	}
}

// TestDeclNameCasePascalCasePasses pins the negative case: every decl
// kind written in canonical PascalCase must NOT raise the warning.
func TestDeclNameCasePascalCasePasses(t *testing.T) {
	mustClean(t, `package x
type MyType { id string }
error NotFound BadName { code string }
enum Priority { Low High }
scalar ID string
service UserService { get ListUsers /u {} }
`)
}

// TestDeclNameCaseExtendDoesNotDoubleReport confirms that an extend
// service with a lower-case target name does NOT re-warn on the
// service identifier (the original decl already carries it). Methods
// added inside the extend block ARE checked because they're new
// names not previously seen.
func TestDeclNameCaseExtendDoesNotDoubleReport(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
service userService {}
extend service userService {
    get listMore /m {}
}`))
	// Two warnings expected: one for the original `service userService`
	// (lower-case service name), one for `listMore` (lower-case method
	// added in the extend block). The extend's mention of `userService`
	// must not produce a third.
	count := 0
	for _, d := range diags {
		if d.Code == CodeDeclNameCase {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected exactly 2 name-case warnings (service + method); got %d (%v)", count, codes(diags))
	}
}

// TestDeclNameCaseEmptyNameSkipped asserts the empty-name guard via
// a direct call to the helper — bypassing parser fixtures that would
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
