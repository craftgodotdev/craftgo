package lsp

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/semantic"
)

const eventsDSL = `package orders

type OrderPlacedPayload {
	orderId string
}

@doc("Emitted once an order is accepted.")
event OrderPlaced {
	payload OrderPlacedPayload
}

service OrderService {
	get Fetch /orders { response OrderPlacedPayload }

	post Place /orders {
		request  OrderPlacedPayload
		response OrderPlacedPayload
	}
}
`

func TestHoverOnMemberKeywords(t *testing.T) {
	cases := map[string]string{
		"event":    "a contract this design declares",
		"payload":  "the type an event contract carries",
		"request":  "binds and validates",
		"response": "the framework encodes it",
	}
	for needle, want := range cases {
		t.Run(needle, func(t *testing.T) {
			got := mustHoverAt(t, "t.craftgo", eventsDSL, needle)
			if !strings.Contains(got, want) {
				t.Errorf("hover on %q = %q, want it to mention %q", needle, got, want)
			}
		})
	}
}

// A decorator zone belongs to the declaration that follows it, so the
// completion list offers event decorators above a file-level `event` and
// method decorators above a verb inside a service body.
func TestDecoratorSiteLevelFollowsTheDeclaration(t *testing.T) {
	view := parseSnapshot("t.craftgo", eventsDSL)
	for _, c := range []struct {
		needle string
		want   semantic.Level
	}{
		{"event", semantic.LvlEvent},
		{"post", semantic.LvlMethod},
	} {
		pos := findToken(t, view, c.needle)
		above := protocol.Position{Line: pos.Line - 1, Character: 0}
		if got := guessLevel(view, above); got != c.want {
			t.Errorf("level above %q = %s, want %s", c.needle, got.Name(), c.want.Name())
		}
	}
}

// The outline lists a contract beside the services, so a file reads the
// same in the editor as in the source.
func TestDocumentSymbolsListContracts(t *testing.T) {
	view := parseSnapshot("t.craftgo", eventsDSL)
	details := map[string]string{}
	for _, sym := range documentSymbols(view) {
		details[sym.Name] = sym.Detail
	}
	if got := details["OrderPlaced"]; got != "event OrderPlaced (OrderPlacedPayload)" {
		t.Errorf("event symbol detail = %q", got)
	}
	if _, ok := details["OrderService"]; !ok {
		t.Error("the service symbol is missing")
	}
}

// `@contract` is event-level, so an event's decorator zone offers it and
// the method-only ones stay out.
func TestEventDecoratorCompletions(t *testing.T) {
	src := `package orders

type P { id string }

@
event OrderPlaced {
	payload P
}
`
	view := parseSnapshot("t.craftgo", src)
	pos := findToken(t, view, "@")
	pos.Character++
	have := map[string]bool{}
	for _, item := range decoratorCompletions(view, pos, "") {
		have[item.Label] = true
	}
	for _, want := range []string{"contract", "doc"} {
		if !have[want] {
			t.Errorf("event completions missing @%s: %v", want, have)
		}
	}
	if have["timeout"] {
		t.Error("event completions must not offer the method-only @timeout")
	}
	for _, gone := range []string{"consumerGroup", "consumeMiddlewares"} {
		if have[gone] {
			t.Errorf("event completions still offer @%s", gone)
		}
	}
}

// `event` and `payload` are legal field names, so the keyword docs and
// the type-shape classifier must both look at which declaration they sit
// in rather than at the spelling alone.
func TestMemberKeywordsStayFieldNamesInsideATypeBody(t *testing.T) {
	src := `package p

type MyType { id string }

type Holder {
	event   MyType
	payload string
}
`
	view := parseSnapshot("t.craftgo", src)

	// Hover on the field named `event` must not show the keyword doc.
	pos := findToken(t, view, "event")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	if hov := hoverForToken(view, idx, tok); hov != nil && strings.Contains(hov.Contents.Value, "a contract this design declares") {
		t.Errorf("a field named `event` showed the event keyword doc: %q", hov.Contents.Value)
	}

	// Go-to-definition on that field's type must still classify as a
	// type-shape position.
	var typeIdx int
	for i, tk := range view.tokens {
		if tk.Text == "MyType" && i > idx {
			typeIdx = i
			break
		}
	}
	if typeIdx == 0 {
		t.Fatal("no MyType token after the field name")
	}
	if !isTypeShapePosition(view, typeIdx) {
		t.Error("the type of a field named `event` must resolve as a type reference")
	}
}

// An event's payload names a type, so the cursor there resolves like any
// other type reference - the event's own name does not.
func TestPayloadRefIsATypeShapePosition(t *testing.T) {
	view := parseSnapshot("t.craftgo", eventsDSL)
	at := func(text string, nth int) int {
		t.Helper()
		var seen int
		for i, tok := range view.tokens {
			if tok.Text != text {
				continue
			}
			seen++
			if seen == nth {
				return i
			}
		}
		t.Fatalf("token %q #%d not found", text, nth)
		return 0
	}
	if !isTypeShapePosition(view, at("OrderPlacedPayload", 2)) { // the event's payload
		t.Error("a payload reference must resolve as a type reference")
	}
	if isTypeShapePosition(view, at("OrderPlaced", 1)) {
		t.Error("an event's own name must not resolve as a type reference")
	}
}

// Hovering a decorator craftgo has removed shows the migration note the
// diagnostic carries, rather than "unknown decorator" - the editor is
// where an author first meets an unmigrated design.
func TestHoverOnARemovedDecorator(t *testing.T) {
	src := `package orders

type P { id string }

@key(id)
event OrderPlaced {
	payload P
}
`
	v := mustHoverAt(t, "t.craftgo", src, "key")
	for _, want := range []string{"removed", "WithKey"} {
		if !strings.Contains(v, want) {
			t.Errorf("hover does not mention %q: %s", want, v)
		}
	}
}
