package parser

import (
	"reflect"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestFreeCommentHarvestTypeBody pins that comment blocks in a type body
// become FreeComment members at their source lines.
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

// TestFreeCommentFieldDocNotHarvested pins that a field's Doc is not also a
// FreeComment.
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

// TestFreeCommentHarvestService pins comment blocks between methods and inside
// a method body, and that Method.EndPos is the body's closing brace.
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

// TestFreeCommentMethodChainNotHarvested pins that a comment inside a method's
// decorator chain is neither a service member nor a body comment.
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

// TestFreeCommentHarvestFileScope pins that blocks above the package line,
// between declarations and at the end land on File.FreeComments.
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
// declaration's decorator chain is not a file-scope FreeComment.
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

// The comments inside a decorator chain are recorded under the line of the
// decorator, name or keyword below them, for file, declaration, method and
// field chains alike, and none is a free comment.
func TestChainCommentsRecorded(t *testing.T) {
	f := mustParse(t, `@version("1")
// file chain
@doc("d")
package p

@minLength(1)
// type chain

// after a blank line
type Name {
	@minLength(1)
	// field chain
	v string
}

service S {
	@doc("m")
	// method chain
	get A /a {}
}
`)
	want := map[int][]string{
		3:  {"file chain"},
		10: {"type chain", "after a blank line"},
		13: {"field chain"},
		19: {"method chain"},
	}
	if !reflect.DeepEqual(f.ChainComments, want) {
		t.Errorf("ChainComments = %v, want %v", f.ChainComments, want)
	}
	if len(f.FreeComments) != 0 || len(f.Decls[0].(*ast.TypeDecl).Body) != 1 {
		t.Errorf("a chain comment became free: file %#v, type body %#v", f.FreeComments, f.Decls[0].(*ast.TypeDecl).Body)
	}
}

// The comments among the decorators after a field or an enum value are
// recorded under the line of the decorator below them, and none is a free
// comment.
func TestTrailingChainCommentsRecorded(t *testing.T) {
	f := mustParse(t, `package p

type T {
	x string @minLength(1)
	// field chain
		@maxLength(5)
	y string
		// before the first
		@minLength(2)
}

enum E {
	A = 1 @doc("a")
	// value chain
		@deprecated
	B = 2
}
`)
	want := map[int][]string{
		6:  {"field chain"},
		9:  {"before the first"},
		15: {"value chain"},
	}
	if !reflect.DeepEqual(f.ChainComments, want) {
		t.Errorf("ChainComments = %v, want %v", f.ChainComments, want)
	}
	if body, members := f.Decls[0].(*ast.TypeDecl).Body, f.Decls[1].(*ast.EnumDecl).Members; len(body) != 2 || len(members) != 2 {
		t.Errorf("a chain comment became free: type body %#v, enum members %#v", body, members)
	}
}

// In a file without a package, the comment under the decorators it hands to
// the first declaration belongs to their chain, not to that declaration's doc.
func TestForwardedChainCommentIsNoDoc(t *testing.T) {
	f := mustParse(t, "// file doc\n@doc(\"t\")\n// in the chain\ntype T {\n\ty string\n}\n")
	td := f.Decls[0].(*ast.TypeDecl)
	if len(td.Doc) != 0 || len(f.LeadingDoc) != 1 || f.LeadingDoc[0] != "file doc" {
		t.Errorf("Doc = %q, LeadingDoc = %q", td.Doc, f.LeadingDoc)
	}
	if got := f.ChainComments[4]; len(got) != 1 || got[0] != "in the chain" {
		t.Errorf("ChainComments = %v", f.ChainComments)
	}
}

// TestMixinDocCaptured pins that the comment above a mixin is its Doc.
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

// A comment block inside a declaration's or a method's header, before its
// opening brace, is a free comment at the top of its body.
func TestHeaderCommentOpensTheBody(t *testing.T) {
	f := mustParse(t, `package p

type
// in type
T {
	a string
}

enum E
// in enum
{
	A
}

error NotFound
// in error
Gone {
	b string
}

extend
// in extend
service S {
	get A
	// in method
	/a {
		response T
	}
}

event
// in event
Ev {
	payload T
}
`)
	if len(f.FreeComments) != 0 {
		t.Fatalf("a header comment stayed at file scope: %#v", f.FreeComments)
	}
	svc := f.Decls[3].(*ast.ServiceDecl)
	for want, got := range map[string]any{
		"in type":   f.Decls[0].(*ast.TypeDecl).Body[0],
		"in enum":   f.Decls[1].(*ast.EnumDecl).Members[0],
		"in error":  f.Decls[2].(*ast.ErrorDecl).Body[0],
		"in extend": svc.Members[0],
		"in method": firstComment(svc.Methods()[0].BodyComments),
		"in event":  firstComment(f.Decls[4].(*ast.EventDecl).BodyComments),
	} {
		if fc, ok := got.(*ast.FreeComment); !ok || !reflect.DeepEqual(fc.Text, []string{want}) {
			t.Errorf("first member %#v, want the free comment %q", got, want)
		}
	}
}

// firstComment returns the first of fcs, or nil.
func firstComment(fcs []*ast.FreeComment) any {
	if len(fcs) == 0 {
		return nil
	}
	return fcs[0]
}
