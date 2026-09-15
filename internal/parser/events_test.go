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

// `consume` left the reserved-word list with the listener declarations;
// a design still carrying one is told where the listener went rather
// than being handed the generic member error.
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

// An `event` inside a service body used to declare the contract that
// service publishes; the diagnostic says where it belongs now.
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

func TestParseEventRejectsArrayPayload(t *testing.T) {
	p := New("test.craftgo", `package p
event E { payload Order[] }`)
	p.Parse()
	diags := p.Diagnostics()
	if len(diags) == 0 || !strings.Contains(diags[0].Msg, "payload type cannot be a bare array") {
		t.Fatalf("want a bare-array diagnostic, got %v", diags)
	}
}

// The event keywords stay contextual where the grammar leaves no
// ambiguity: a type body member is a field or a mixin, and a keyword
// never spells a mixin, so `event` / `payload` remain legal field names.
// `consume` is an ordinary identifier again and needs no such rule.
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

// A reserved word in a decorator argument slot names a field, not a
// literal - the only reading that leaves `@requiresOneOf(payload, ...)`
// meaningful once `payload` became a keyword.
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

// Reserved words are legal path segments and path-parameter names, so a
// route is unaffected by the keyword table growing.
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
