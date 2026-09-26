package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

func TestParseFileLevelEvent(t *testing.T) {
	ev := firstDecl[*ast.EventDecl](t, `package orders

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
	_, msgs := parseWithErrors(t, `package p
service S {
	consume SendReceipt {
		event shared.OrderPlaced
	}
}`)
	if !strings.Contains(firstMsg(msgs), "a service has no `consume` member") {
		t.Fatalf("want the consume diagnostic, got %v", msgs)
	}
}

// TestParseEventInServiceBodyIsRejected pins that an `event` in a service body
// is told to move to file level.
func TestParseEventInServiceBodyIsRejected(t *testing.T) {
	_, msgs := parseWithErrors(t, `package p
service S {
	event E { payload P }
}`)
	if !strings.Contains(firstMsg(msgs), "`event` is a file-level declaration") {
		t.Fatalf("want a file-level-event diagnostic, got %v", msgs)
	}
}

func TestParseEventRejectsUnknownClause(t *testing.T) {
	_, msgs := parseWithErrors(t, `package p
event E { response R }`)
	if !strings.Contains(firstMsg(msgs), "payload in event body") {
		t.Fatalf("want a payload-clause diagnostic, got %v", msgs)
	}
}

func TestParseEventRejectsDuplicatePayload(t *testing.T) {
	_, msgs := parseWithErrors(t, `package p
event E {
	payload A
	payload B
}`)
	if !strings.Contains(firstMsg(msgs), "duplicate payload clause") {
		t.Fatalf("want a duplicate-payload diagnostic, got %v", msgs)
	}
}

// TestParseEventAcceptsAnArrayPayload pins that `payload Order[]` parses with
// Array set.
func TestParseEventAcceptsAnArrayPayload(t *testing.T) {
	e := firstDecl[*ast.EventDecl](t, `package p
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
	_, msgs := parseWithErrors(t, `package p
event E { payload map<string, int> }`)
	if !strings.Contains(firstMsg(msgs), "expected Ident") {
		t.Fatalf("want a payload-type diagnostic, got %v", msgs)
	}
}

// TestParseEventRejectsNestedArrayPayload pins that `payload Order[][]` is an
// error.
func TestParseEventRejectsNestedArrayPayload(t *testing.T) {
	_, msgs := parseWithErrors(t, `package p
event E { payload Order[][] }`)
	if !strings.Contains(firstMsg(msgs), "payload type cannot be a nested array") {
		t.Fatalf("want a nested-array diagnostic, got %v", msgs)
	}
}

// TestParseEventRejectsOptionalArrayPayload pins that `payload Order[]?` is an
// error.
func TestParseEventRejectsOptionalArrayPayload(t *testing.T) {
	_, msgs := parseWithErrors(t, `package p
event E { payload Order[]? }`)
	if !strings.Contains(firstMsg(msgs), "payload type cannot be optional") {
		t.Fatalf("want an optional-marker diagnostic, got %v", msgs)
	}
}

// TestNewKeywordsStillWorkAsFieldNames pins that `event`, `consume` and
// `payload` are legal field names.
func TestNewKeywordsStillWorkAsFieldNames(t *testing.T) {
	td := firstDecl[*ast.TypeDecl](t, `package p
type T {
	event   string
	consume string
	payload string
}`)
	var names []string
	for _, fl := range ast.Fields(td.Body) {
		names = append(names, fl.Name)
	}
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
	td := firstDecl[*ast.TypeDecl](t, `package p
@requiresOneOf(payload, event)
type T {
	payload string?
	event   string?
}`)
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
	sd := firstDecl[*ast.ServiceDecl](t, `package p
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
