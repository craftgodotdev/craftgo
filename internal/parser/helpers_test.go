package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// mustParse parses src and fails the test on any diagnostic.
func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	p := New("test", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	return f
}

// renderMembers renders a type body as `[id string; name string]` for failure
// messages.
func renderMembers(ms []ast.TypeMember) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		switch v := m.(type) {
		case *ast.Field:
			parts[i] = v.Name + " " + v.Type.String()
		case *ast.Mixin:
			parts[i] = v.Ref.String()
		default:
			parts[i] = "?"
		}
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// firstDecl parses src without diagnostics and returns its first declaration
// of type T.
func firstDecl[T ast.Decl](t *testing.T, src string) T {
	t.Helper()
	for _, d := range mustParse(t, src).Decls {
		if v, ok := d.(T); ok {
			return v
		}
	}
	var zero T
	t.Fatalf("no %T in %q", zero, src)
	return zero
}

// parseWithErrors parses src and returns the file and the message of each
// diagnostic.
func parseWithErrors(t *testing.T, src string) (*ast.File, []string) {
	t.Helper()
	p := New("test", src)
	f := p.Parse()
	var msgs []string
	for _, d := range p.Diagnostics() {
		msgs = append(msgs, d.Msg)
	}
	return f, msgs
}

// firstMsg returns the first of msgs, or "" when there is none.
func firstMsg(msgs []string) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0]
}
