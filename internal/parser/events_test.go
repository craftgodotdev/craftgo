package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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

func TestParseFileLevelEvent(t *testing.T) {
	ev := parseEvent(t, `package orders

// Emitted once an order is accepted.
@contract("order.placed.v2")
event OrderPlaced {
	payload OrderPlacedPayload
}

service OrderService {
	post PlaceOrder /orders { request Req  response Resp }
}`)
	if ev.Name != "OrderPlaced" {
		t.Errorf("event name = %q", ev.Name)
	}
	if ev.Payload == nil || ev.Payload.Type.Name.String() != "OrderPlacedPayload" {
		t.Errorf("payload = %+v", ev.Payload)
	}
	if len(ev.Decorators) != 1 || ev.Decorators[0].Name != "contract" {
		t.Errorf("decorators = %+v", ev.Decorators)
	}
	if len(ev.Doc) != 1 {
		t.Errorf("doc = %v", ev.Doc)
	}
}

// TestParseConsumeInServiceBodyIsRejected pins the dedicated diagnostic for
// `consume` in a service body.
func TestParseConsumeInServiceBodyIsRejected(t *testing.T) {
	p := New("test.craftgo", `package p
service S {
	consume SendReceipt {
		event shared.OrderPlaced
	}
}`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "`consume` is no longer part of the DSL") {
		t.Fatalf("want a consume-removed diagnostic, got %v", diags)
	}
}

// TestParseEventInServiceBodyIsRejected pins that an `event` in a service body
// is told to move to file level.
func TestParseEventInServiceBodyIsRejected(t *testing.T) {
	p := New("test.craftgo", `package p
service S {
	event E { payload P }
}`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "`event` is a file-level declaration") {
		t.Fatalf("want a file-level-event diagnostic, got %v", diags)
	}
}

func TestParseEventRejectsUnknownClause(t *testing.T) {
	p := New("test.craftgo", `package p
event E { response R }`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "payload in event body") {
		t.Fatalf("want a payload-clause diagnostic, got %v", diags)
	}
}

func TestParseEventRejectsDuplicatePayload(t *testing.T) {
	p := New("test.craftgo", `package p
event E {
	payload A
	payload B
}`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "duplicate payload clause") {
		t.Fatalf("want a duplicate-payload diagnostic, got %v", diags)
	}
}

// TestParseEventAcceptsAnArrayPayload pins that `payload Order[]` parses with
// Array set.
func TestParseEventAcceptsAnArrayPayload(t *testing.T) {
	e := parseEvent(t, `package p
event E { payload Order[] }`)
	if e.Payload == nil || e.Payload.Type == nil {
		t.Fatalf("payload did not parse: %+v", e)
	}
	if got := e.Payload.Type.Name.String(); got != "Order" {
		t.Errorf("payload type = %q, want the element type Order", got)
	}
	if !e.Payload.Array {
		t.Error("the `[]` suffix was dropped - the payload reads as a single Order")
	}
}

// TestParseEventRejectsMapPayload pins that a map payload is an error.
func TestParseEventRejectsMapPayload(t *testing.T) {
	p := New("test.craftgo", `package p
event E { payload map<string, int> }`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "expected Ident") {
		t.Fatalf("want a payload-type diagnostic, got %v", diags)
	}
}

// TestParseEventRejectsNestedArrayPayload pins that `payload Order[][]` is an
// error.
func TestParseEventRejectsNestedArrayPayload(t *testing.T) {
	p := New("test.craftgo", `package p
event E { payload Order[][] }`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "payload type cannot be a nested array") {
		t.Fatalf("want a nested-array diagnostic, got %v", diags)
	}
}

// TestParseEventRejectsOptionalArrayPayload pins that `payload Order[]?` is an
// error.
func TestParseEventRejectsOptionalArrayPayload(t *testing.T) {
	p := New("test.craftgo", `package p
event E { payload Order[]? }`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "payload type cannot be optional") {
		t.Fatalf("want an optional-marker diagnostic, got %v", diags)
	}
}

// TestNewKeywordsStillWorkAsFieldNames pins that `event`, `consume` and
// `payload` are legal field names.
func TestNewKeywordsStillWorkAsFieldNames(t *testing.T) {
	p := New("test.craftgo", `package p
type T {
	event   string
	consume string
	payload string
}`)
	f := p.Parse()
	if diags := p.Diagnostics(); len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	td := f.Decls[0].(*ast.TypeDecl)
	var names []string
	ast.EachField(td.Body, func(fl *ast.Field) bool {
		names = append(names, fl.Name)
		return true
	})
	want := []string{"event", "consume", "payload"}
	for i := range want {
		if i >= len(names) || names[i] != want[i] {
			t.Fatalf("field names = %v, want %v", names, want)
		}
	}
}

// TestKeywordSpellingsWorkAsDecoratorArguments pins that a reserved word in a
// decorator argument is an identifier.
func TestKeywordSpellingsWorkAsDecoratorArguments(t *testing.T) {
	p := New("test.craftgo", `package p
@requiresOneOf(payload, event)
type T {
	payload string?
	event   string?
}`)
	f := p.Parse()
	if diags := p.Diagnostics(); len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	td := f.Decls[0].(*ast.TypeDecl)
	args := td.Decorators[0].Args
	if len(args) != 2 {
		t.Fatalf("args = %d, want 2", len(args))
	}
	for i, want := range []string{"payload", "event"} {
		id, ok := args[i].Value.(*ast.IdentExpr)
		if !ok || id.Name.String() != want {
			t.Fatalf("arg %d = %#v, want ident %q", i, args[i].Value, want)
		}
	}
}

// TestNewKeywordsStillWorkInPaths pins that reserved words work as path
// segments and parameter names.
func TestNewKeywordsStillWorkInPaths(t *testing.T) {
	sd := parseService(t, `package p
service S {
	get Read /event/{payload} {}
}`)
	path := sd.Methods()[0].Path
	if len(path.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(path.Segments))
	}
	if path.Segments[0].Literal != "event" || !path.Segments[1].Param || path.Segments[1].Literal != "payload" {
		t.Fatalf("path = %+v", path.Segments)
	}
}
