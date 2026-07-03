// Tests for free-floating comment harvesting: every leading comment inside
// a body that no Doc field claims must surface as a position-accurate
// [ast.FreeComment], and claimed comments must never be harvested twice.
package parser

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestFreeCommentHarvestTypeBody pins mid-body section dividers and closing
// notes inside a type body: each becomes a FreeComment member at its source
// slot, carrying the position of its first `//` line.
func TestFreeCommentHarvestTypeBody(t *testing.T) {
	f := mustParse(t, `package p

type User {
	// Section: Identity

	id string

	// Section: Contact
	// spans two lines

	email string

	// closing note
}
`)
	td, ok := f.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected TypeDecl, got %T", f.Decls[0])
	}
	if len(td.Body) != 5 {
		t.Fatalf("expected 5 members (2 fields + 3 comments), got %d: %#v", len(td.Body), td.Body)
	}
	fc0, ok := td.Body[0].(*ast.FreeComment)
	if !ok || fc0.Text[0] != "Section: Identity" {
		t.Fatalf("member 0: expected FreeComment 'Section: Identity', got %#v", td.Body[0])
	}
	if fc0.Pos.Line != 4 {
		t.Errorf("member 0: expected real position line 4, got %d", fc0.Pos.Line)
	}
	if _, ok := td.Body[1].(*ast.Field); !ok {
		t.Fatalf("member 1: expected Field, got %#v", td.Body[1])
	}
	fc2, ok := td.Body[2].(*ast.FreeComment)
	if !ok || len(fc2.Text) != 2 || fc2.Text[1] != "spans two lines" {
		t.Fatalf("member 2: expected two-line FreeComment, got %#v", td.Body[2])
	}
	fc4, ok := td.Body[4].(*ast.FreeComment)
	if !ok || fc4.Text[0] != "closing note" {
		t.Fatalf("member 4: expected closing-note FreeComment, got %#v", td.Body[4])
	}
	if fc4.Pos.Line != 13 {
		t.Errorf("closing note: expected the comment's own line 13, got %d", fc4.Pos.Line)
	}
}

// TestFreeCommentFieldDocNotHarvested pins that a comment block claimed as a
// field's Doc stays a Doc - it must not double as a FreeComment member.
func TestFreeCommentFieldDocNotHarvested(t *testing.T) {
	f := mustParse(t, `package p

type User {
	// attached doc
	id string
}
`)
	td := f.Decls[0].(*ast.TypeDecl)
	if len(td.Body) != 1 {
		t.Fatalf("expected 1 member, got %d: %#v", len(td.Body), td.Body)
	}
	fd, ok := td.Body[0].(*ast.Field)
	if !ok || len(fd.Doc) != 1 || fd.Doc[0] != "attached doc" {
		t.Fatalf("expected field with doc, got %#v", td.Body[0])
	}
}

// TestFreeCommentHarvestEnum pins section dividers between enum values.
func TestFreeCommentHarvestEnum(t *testing.T) {
	f := mustParse(t, `package p

enum Status {
	Active

	// terminal states

	Done
	Cancelled
}
`)
	ed := f.Decls[0].(*ast.EnumDecl)
	if len(ed.Members) != 4 {
		t.Fatalf("expected 4 members, got %d", len(ed.Members))
	}
	fc, ok := ed.Members[1].(*ast.FreeComment)
	if !ok || fc.Text[0] != "terminal states" {
		t.Fatalf("member 1: expected FreeComment, got %#v", ed.Members[1])
	}
	if len(ed.EnumValues()) != 3 {
		t.Errorf("EnumValues must skip comments: got %d", len(ed.EnumValues()))
	}
}

// TestFreeCommentHarvestService pins service-level blocks between methods
// plus method-body comments (above request / above the closing brace),
// and that Method.EndPos records the body's closing brace.
func TestFreeCommentHarvestService(t *testing.T) {
	f := mustParse(t, `package p

service Things {
	// section: reads

	get list /things {
		// above request
		request Req

		// above method rbrace
	}

	// between methods

	post create /things {
		request Req
	}
}
`)
	sd := f.Decls[0].(*ast.ServiceDecl)
	if len(sd.Members) != 4 {
		t.Fatalf("expected 4 members (2 comments + 2 methods), got %d", len(sd.Members))
	}
	if fc, ok := sd.Members[0].(*ast.FreeComment); !ok || fc.Text[0] != "section: reads" {
		t.Fatalf("member 0: expected FreeComment 'section: reads', got %#v", sd.Members[0])
	}
	m1, ok := sd.Members[1].(*ast.Method)
	if !ok {
		t.Fatalf("member 1: expected Method, got %#v", sd.Members[1])
	}
	if len(m1.BodyComments) != 2 {
		t.Fatalf("expected 2 body comments in method list, got %#v", m1.BodyComments)
	}
	if m1.BodyComments[0].Text[0] != "above request" || m1.BodyComments[1].Text[0] != "above method rbrace" {
		t.Errorf("body comments out of order: %#v", m1.BodyComments)
	}
	if m1.EndPos.Line != 11 {
		t.Errorf("EndPos: expected closing brace line 11, got %d", m1.EndPos.Line)
	}
	if fc, ok := sd.Members[2].(*ast.FreeComment); !ok || fc.Text[0] != "between methods" {
		t.Fatalf("member 2: expected FreeComment 'between methods', got %#v", sd.Members[2])
	}
	if len(sd.Methods()) != 2 {
		t.Errorf("Methods must skip comments: got %d", len(sd.Methods()))
	}
}

// TestFreeCommentMethodChainNotHarvested pins that a comment inside a
// method's decorator chain is claimed for the formatter's inter-decorator
// recovery - it must not surface as a service member or body comment (that
// would print it twice).
func TestFreeCommentMethodChainNotHarvested(t *testing.T) {
	f := mustParse(t, `package p

service Things {
	@doc("get thing")
	// chain note
	@status(200)
	get list /things {
		request Req
	}
}
`)
	sd := f.Decls[0].(*ast.ServiceDecl)
	if len(sd.Members) != 1 {
		t.Fatalf("expected only the method, got %d members: %#v", len(sd.Members), sd.Members)
	}
	m := sd.Members[0].(*ast.Method)
	if len(m.BodyComments) != 0 {
		t.Errorf("chain comment leaked into BodyComments: %#v", m.BodyComments)
	}
}

// TestFreeCommentHarvestFileScope pins file-scope blocks: detached from the
// package line, between declarations, and after the last declaration - all
// land on File.FreeComments with real positions, never on a Doc field.
func TestFreeCommentHarvestFileScope(t *testing.T) {
	f := mustParse(t, `// above package, detached

package p

type A {
	id string
}

// between decls

type B {
	id string
}

// end of file note
`)
	if len(f.FreeComments) != 3 {
		t.Fatalf("expected 3 file-scope blocks, got %d: %#v", len(f.FreeComments), f.FreeComments)
	}
	wantText := []string{"above package, detached", "between decls", "end of file note"}
	wantLine := []int{1, 9, 15}
	for i, fc := range f.FreeComments {
		if fc.Text[0] != wantText[i] {
			t.Errorf("block %d: text %q, want %q", i, fc.Text[0], wantText[i])
		}
		if fc.Pos.Line != wantLine[i] {
			t.Errorf("block %d: line %d, want %d", i, fc.Pos.Line, wantLine[i])
		}
	}
}

// TestFreeCommentDeclChainNotHarvested pins that a comment inside a
// top-level declaration's decorator chain stays with the formatter's
// inter-decorator recovery - it must not also surface on File.FreeComments.
func TestFreeCommentDeclChainNotHarvested(t *testing.T) {
	f := mustParse(t, `package p

@minLength(1)
// chain note
type Name {
	v string
}
`)
	if len(f.FreeComments) != 0 {
		t.Errorf("chain comment leaked into File.FreeComments: %#v", f.FreeComments)
	}
}

// TestMixinDocCaptured pins that a `//` block above a mixin reference is
// retained on the Mixin node instead of being dropped.
func TestMixinDocCaptured(t *testing.T) {
	f := mustParse(t, `package p

type User {
	// audit fields shared by all entities
	shared.Audit
	id string
}
`)
	td := f.Decls[0].(*ast.TypeDecl)
	mx, ok := td.Body[0].(*ast.Mixin)
	if !ok {
		t.Fatalf("expected Mixin first, got %#v", td.Body[0])
	}
	if len(mx.Doc) != 1 || mx.Doc[0] != "audit fields shared by all entities" {
		t.Errorf("mixin doc not captured: %#v", mx.Doc)
	}
}
