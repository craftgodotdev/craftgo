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

service OrderService {
	@doc("Emitted once an order is accepted.")
	event OrderPlaced {
		payload OrderPlacedPayload
	}

	consume Mirror {
		event OrderPlaced
	}
}
`

func TestHoverOnMemberKeywords(t *testing.T) {
	cases := map[string]string{
		"event":   "the contract this service publishes",
		"consume": "this service handles",
		"payload": "the type an event contract carries",
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

// A decorator zone inside a service body belongs to the member that
// follows it, so the completion list offers event decorators above an
// `event` and method decorators above a verb.
func TestDecoratorSiteLevelFollowsTheMember(t *testing.T) {
	view := parseSnapshot("t.craftgo", eventsDSL)
	cases := []struct {
		needle string
		want   semantic.Level
	}{
		{"event", semantic.LvlEvent},
		{"consume", semantic.LvlConsumer},
	}
	for _, c := range cases {
		pos := findToken(t, view, c.needle)
		// The decorator zone sits on the line above the member keyword.
		above := protocol.Position{Line: pos.Line - 1, Character: 0}
		if got := nextServiceMemberLevel(view, above); got != c.want {
			t.Errorf("level above %q = %s, want %s", c.needle, got.Name(), c.want.Name())
		}
	}
}

// The outline lists events and consumers alongside methods, so a service
// body reads the same in the editor as in the source.
func TestDocumentSymbolsIncludeEventsAndConsumers(t *testing.T) {
	view := parseSnapshot("t.craftgo", eventsDSL)
	var service *protocol.DocumentSymbol
	for i, sym := range documentSymbols(view) {
		if sym.Name == "OrderService" {
			service = &documentSymbols(view)[i]
		}
	}
	if service == nil {
		t.Fatal("no service symbol")
	}
	details := map[string]string{}
	for _, child := range service.Children {
		details[child.Name] = child.Detail
	}
	if got := details["OrderPlaced"]; got != "event OrderPlaced (OrderPlacedPayload)" {
		t.Errorf("event symbol detail = %q", got)
	}
	if got := details["Mirror"]; got != "consume Mirror (OrderPlaced)" {
		t.Errorf("consumer symbol detail = %q", got)
	}
}

// `@contract` is event-level, so an event's decorator zone offers it and
// the method-only ones stay out.
func TestEventDecoratorCompletions(t *testing.T) {
	src := `package orders

type P { id string }

service OrderService {
	@
	event OrderPlaced {
		payload P
	}
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
}

// `@consumerGroup` is consumer-level as well as service-level, so a
// consumer's decorator zone offers it.
func TestConsumerDecoratorCompletions(t *testing.T) {
	src := `package orders

type P { id string }

service OrderService {
	event OrderPlaced {
		payload P
	}

	@
	consume Mirror {
		event OrderPlaced
	}
}
`
	view := parseSnapshot("t.craftgo", src)
	pos := findToken(t, view, "@")
	pos.Character++
	have := map[string]bool{}
	for _, item := range decoratorCompletions(view, pos, "") {
		have[item.Label] = true
	}
	if !have["consumerGroup"] {
		t.Errorf("consumer completions missing @consumerGroup: %v", have)
	}
	if have["contract"] {
		t.Error("consumer completions must not offer the event-only @contract")
	}
}

// Go-to-definition on a consumer's event reference lands on the event,
// not on a same-named type: events have their own namespace, so the
// generic declaration lookup cannot resolve one.
func TestDefinitionOnAConsumerEventRef(t *testing.T) {
	src := `package orders

type OrderPlaced { id string }

service OrderService {
	event OrderPlaced { payload OrderPlaced }
	consume Mirror { event OrderPlaced }
}
`
	view := parseSnapshot("t.craftgo", src)
	// The fourth `OrderPlaced` is the one in the consumer's event clause
	// (type decl, event decl, payload ref, then the consumer's ref).
	var idx, count int
	for i, tok := range view.tokens {
		if tok.Text != "OrderPlaced" {
			continue
		}
		count++
		if count == 4 {
			idx = i
			break
		}
	}
	if count < 4 {
		t.Fatalf("expected 4 OrderPlaced tokens, got %d", count)
	}
	if !isConsumerEventPosition(view, idx) {
		t.Fatal("the consumer's event clause was not recognised")
	}
	// The same spelling in a payload position stays a type reference.
	var payloadIdx, seen int
	for i, tok := range view.tokens {
		if tok.Text != "OrderPlaced" {
			continue
		}
		seen++
		if seen == 3 {
			payloadIdx = i
			break
		}
	}
	if isConsumerEventPosition(view, payloadIdx) {
		t.Error("a payload reference must not resolve as an event reference")
	}
	if !isTypeShapePosition(view, payloadIdx) {
		t.Error("a payload reference must resolve as a type reference")
	}
}

// `event`, `consume` and `payload` are legal field names, so the
// keyword docs and the type-shape classifier must both look at which
// declaration they sit in rather than at the spelling alone.
func TestMemberKeywordsStayFieldNamesInsideATypeBody(t *testing.T) {
	src := `package p

type MyType { id string }

type Holder {
	event   MyType
	consume string
	payload string
}
`
	view := parseSnapshot("t.craftgo", src)

	// Hover on the field named `event` must not show the consumer doc.
	pos := findToken(t, view, "event")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	if hov := hoverForToken(view, idx, tok); hov != nil && strings.Contains(hov.Contents.Value, "this service handles") {
		t.Errorf("a field named `event` showed the consumer keyword doc: %q", hov.Contents.Value)
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

// A consumer usually references a contract another package declares, so
// the cursor lands on a qualified `pkg.Event`. Both halves have to resolve
// - the reader clicks the event name far more often than the qualifier -
// and the walk back to the clause keyword must clear the whole reference:
// stepping one token at a time stops on the dot, where neither neighbour
// is the keyword.
func TestDefinitionOnAQualifiedConsumerEventRef(t *testing.T) {
	src := `package notifications

type Local { id string }

service NotificationService {
	consume SendReceipt { event orders.Placed }
	post Store /store { request Local  response Local }
}
`
	view := parseSnapshot("t.craftgo", src)
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
	for _, c := range []struct {
		name string
		idx  int
		want bool
	}{
		{"the event name", at("Placed", 1), true},
		{"the package qualifier", at("orders", 1), true},
		{"a request type", at("Local", 2), false},
		{"the consumer name", at("SendReceipt", 1), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := isConsumerEventPosition(view, c.idx); got != c.want {
				t.Errorf("isConsumerEventPosition = %v, want %v", got, c.want)
			}
		})
	}
	// Whichever half is clicked, the name looked up is the whole reference.
	for _, idx := range []int{at("Placed", 1), at("orders", 1)} {
		if got := qualifiedNameAt(view, idx); got != "orders.Placed" {
			t.Errorf("qualifiedNameAt = %q, want %q", got, "orders.Placed")
		}
	}
}

// Hovering a decorator craftgo has removed shows the migration note the
// diagnostic carries, rather than "unknown decorator" - the editor is
// where an author first meets an unmigrated design.
func TestHoverOnARemovedDecorator(t *testing.T) {
	src := `package orders

type P { id string }

service OrderService {
	@key(id)
	event OrderPlaced {
		payload P
	}
}
`
	v := mustHoverAt(t, "t.craftgo", src, "key")
	for _, want := range []string{"removed", "WithKey"} {
		if !strings.Contains(v, want) {
			t.Errorf("hover does not mention %q: %s", want, v)
		}
	}
}
