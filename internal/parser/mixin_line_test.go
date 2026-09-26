package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestMixinTrailingDecoratorIsReported pins that a decorator on a mixin's line
// is reported rather than given to the field below.
func TestMixinTrailingDecoratorIsReported(t *testing.T) {
	f, msgs := parseWithErrors(t, "package p\ntype A {\n\tuser string S @default(\"\")\n\tname string\n}\n")
	if len(msgs) != 1 || !strings.Contains(msgs[0], "mixin") {
		t.Fatalf("diagnostics = %v, want one mixin diagnostic", msgs)
	}
	td := f.Decls[0].(*ast.TypeDecl)
	if got := renderMembers(td.Body); !strings.Contains(got, "name") {
		t.Fatalf("members = %s", got)
	}
	last, ok := td.Body[len(td.Body)-1].(*ast.Field)
	if !ok || last.Name != "name" || len(last.Decorators) != 0 || last.Type.Optional {
		t.Errorf("field below the mixin must stay untouched, got %+v", last)
	}
}

// TestCompactMembersStillParse pins that several members may share a line.
func TestCompactMembersStillParse(t *testing.T) {
	for _, src := range []string{
		"package p\ntype A { Profile  name string  age int? }\n",
		"package p\ntype A { id string  name string }\n",
		"package p\ntype A { shared.Profile  name string @length(1, 80) }\n",
	} {
		f, msgs := parseWithErrors(t, src)
		if len(msgs) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", src, msgs)
			continue
		}
		if n := len(f.Decls[0].(*ast.TypeDecl).Body); n < 2 {
			t.Errorf("%q: want at least 2 members, got %d", src, n)
		}
	}
}
