package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	p := New("test", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	return f
}

// mustParseTypeDecl parses src without diagnostics and returns its first
// declaration as a TypeDecl.
func mustParseTypeDecl(t *testing.T, src string) *ast.TypeDecl {
	t.Helper()
	f := mustParse(t, src)
	if len(f.Decls) == 0 {
		t.Fatalf("expected at least one decl, got none\nsrc: %s", src)
	}
	td, ok := f.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("Decls[0] = %T, want *ast.TypeDecl\nsrc: %s", f.Decls[0], src)
	}
	return td
}

// stringsEqual compares string slices, treating nil and empty as equal.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderMembers renders a type body as `[id string; name string]` for failure
// messages.
func renderMembers(ms []ast.TypeMember) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		switch v := m.(type) {
		case *ast.Field:
			parts[i] = v.Name + " " + renderTypeRef(v.Type)
		case *ast.Mixin:
			if v.Ref != nil {
				parts[i] = v.Ref.Name.String()
				if len(v.Ref.Args) > 0 {
					inner := make([]string, len(v.Ref.Args))
					for j, a := range v.Ref.Args {
						inner[j] = renderTypeRef(a)
					}
					parts[i] += "<" + strings.Join(inner, ", ") + ">"
				}
			}
		default:
			parts[i] = "?"
		}
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func renderTypeRef(t *ast.TypeRef) string {
	if t == nil {
		return "?"
	}
	if t.Map != nil {
		return "map<" + renderTypeRef(t.Map.Key) + ", " + renderTypeRef(t.Map.Value) + ">"
	}
	out := ""
	if t.Named != nil {
		out = t.Named.Name.String()
		if len(t.Named.Args) > 0 {
			inner := make([]string, len(t.Named.Args))
			for i, a := range t.Named.Args {
				inner[i] = renderTypeRef(a)
			}
			out += "<" + strings.Join(inner, ", ") + ">"
		}
	}
	if t.Array {
		out += "[]"
	}
	if t.Optional {
		out += "?"
	}
	return out
}

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

// parseSrc parses src and fails the test on any diagnostic.
func parseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	p := New("k.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse errors: %v", d)
	}
	return f
}

// pathStr renders a Path as source text.
func pathStr(p *ast.Path) string {
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteByte('{')
			sb.WriteString(s.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}

// parseService parses src and returns the first service declaration.
func parseService(t *testing.T, src string) *ast.ServiceDecl {
	t.Helper()
	p := New("test.craftgo", src)
	f := p.Parse()
	if diags := p.Diagnostics(); len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, d := range f.Decls {
		if sd, ok := d.(*ast.ServiceDecl); ok {
			return sd
		}
	}
	t.Fatalf("no service declaration in %q", src)
	return nil
}

// parseEvent parses src and returns the first event declaration.
func parseEvent(t *testing.T, src string) *ast.EventDecl {
	t.Helper()
	p := New("test.craftgo", src)
	f := p.Parse()
	if diags := p.Diagnostics(); len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, d := range f.Decls {
		if ed, ok := d.(*ast.EventDecl); ok {
			return ed
		}
	}
	t.Fatalf("no event declaration in %q", src)
	return nil
}

func firstMsg(msgs []string) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0]
}
