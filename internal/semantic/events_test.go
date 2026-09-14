package semantic

import (
	"slices"
	"strings"
	"testing"
)

const ordersDesign = `package orders
type OrderPlacedPayload {
	orderId string
	total   int64
	tags    string[]
	nested  Nested
}
type Nested { a string }
service OrderService {
	event OrderPlaced { payload OrderPlacedPayload }
}`

func TestEventResolvesContractAndPayload(t *testing.T) {
	pkg := expectClean(t, ordersDesign)
	if len(pkg.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(pkg.Events))
	}
	proj, _ := AnalyzeProject(parseFiles(t, ordersDesign), Options{})
	events := proj.Events()
	if len(events) != 1 {
		t.Fatalf("project events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Contract != "orders.OrderPlaced" {
		t.Errorf("contract = %q, want orders.OrderPlaced", ev.Contract)
	}
	if ev.Service != "OrderService" || ev.Package != "orders" {
		t.Errorf("home = %s/%s", ev.Package, ev.Service)
	}
	if ev.PayloadPkg != "orders" || ev.PayloadName != "OrderPlacedPayload" || ev.Payload == nil {
		t.Errorf("payload = %s.%s (%v)", ev.PayloadPkg, ev.PayloadName, ev.Payload)
	}
}

func TestContractDecoratorOverridesTheDerivedName(t *testing.T) {
	src := `package orders
type P { id string }
service S {
	@contract("order.placed.v2")
	event OrderPlaced { payload P }
}`
	expectClean(t, src)
	proj, _ := AnalyzeProject(parseFiles(t, src), Options{})
	if got := proj.Events()[0].Contract; got != "order.placed.v2" {
		t.Errorf("contract = %q", got)
	}
}

func TestEventRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		msg  string
	}{
		{
			name: "payload missing",
			src:  "package p\nservice S {\n\tevent E {}\n}",
			code: CodeEventPayloadMissing,
			msg:  "has no payload",
		},
		{
			name: "payload is not a struct",
			src:  "package p\nenum E1 { A }\nservice S {\n\tevent E { payload E1 }\n}",
			code: CodeEventPayloadKind,
			msg:  "not a struct type",
		},
		{
			name: "empty contract name",
			src:  `package p` + "\n" + `type P { id string }` + "\n" + `service S {` + "\n\t" + `@contract("")` + "\n\t" + `event E { payload P }` + "\n" + `}`,
			code: CodeEventContractFormat,
			msg:  "without whitespace",
		},
		{
			name: "consumer without an event",
			src:  "package p\nservice S {\n\tconsume C {}\n}",
			code: CodeConsumerEventMissing,
			msg:  "has no event",
		},
		{
			name: "duplicate event name in a package",
			src: `package p
type P { id string }
service A { event E { payload P } }
service B { event E { payload P } }`,
			code: CodeEventDuplicate,
			msg:  "a consumer names an event by this identifier",
		},
		{
			name: "duplicate consumer name in one service",
			src: `package p
type P { id string }
service S {
	event E { payload P }
	consume C { event E }
	consume C { event E }
}`,
			code: CodeConsumerDuplicateName,
			msg:  `duplicate consumer "C" in service "S"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := expectDiag(t, c.src, c.code)
			if !strings.Contains(d.Msg, c.msg) {
				t.Errorf("msg = %q, want it to contain %q", d.Msg, c.msg)
			}
		})
	}
}

// An event and its payload may share a name: they live in separate
// namespaces, and naming the contract after the shape it carries is the
// common case.
func TestEventAndTypeShareANamespaceFreely(t *testing.T) {
	expectClean(t, `package p
type OrderPlaced { id string }
service S {
	event OrderPlaced { payload OrderPlaced }
}`)
}

func TestConsumerResolvesAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"orders/orders.craftgo": `package orders
type P { id string }
service OrderService {
	event OrderPlaced { payload P }
}`,
		"notify/notify.craftgo": `package notify
service NotificationService {
	consume SendReceipt { event orders.OrderPlaced }
}`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	consumers := proj.Consumers()
	if len(consumers) != 1 {
		t.Fatalf("consumers = %d, want 1", len(consumers))
	}
	c := consumers[0]
	if c.Event.Contract != "orders.OrderPlaced" {
		t.Errorf("resolved contract = %q", c.Event.Contract)
	}
	if c.Event.PayloadPkg != "orders" || c.Event.PayloadName != "P" {
		t.Errorf("resolved payload = %s.%s", c.Event.PayloadPkg, c.Event.PayloadName)
	}
	if c.Service != "NotificationService" || c.Package != "notify" {
		t.Errorf("home = %s/%s", c.Package, c.Service)
	}
}

func TestConsumerRejectsAnUnknownEvent(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"notify/notify.craftgo": `package notify
service NotificationService {
	consume SendReceipt { event orders.Nowhere }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeConsumerEventUnknown) == nil {
		t.Fatalf("want %s, got %v", CodeConsumerEventUnknown, codes(diags))
	}
}

// Two events in different packages may resolve to one contract name only
// through `@contract`; publisher and consumer could not tell them apart
// on the wire, so it is rejected.
func TestContractCollisionAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/a.craftgo": `package a
type P { id string }
service A {
	@contract("shared.Thing")
	event One { payload P }
}`,
		"b/b.craftgo": `package b
type Q { id string }
service B {
	@contract("shared.Thing")
	event Two { payload Q }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeEventContractCollision) == nil {
		t.Fatalf("want %s, got %v", CodeEventContractCollision, codes(diags))
	}
}

// Decorator placement is registry-driven: a method decorator on an event
// (or the reverse) is rejected by the same pass that guards every other
// site.
func TestEventDecoratorPlacement(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service S {
	@contract("x.v1")
	get Read /r { response P }
}`, CodeDecoratorPlacement)
	if !strings.Contains(d.Msg, "@contract") || !strings.Contains(d.Msg, "method") {
		t.Errorf("msg = %q", d.Msg)
	}
	d = expectDiag(t, `package p
type P { id string }
service S {
	@timeout(5s)
	event E { payload P }
}`, CodeDecoratorPlacement)
	if !strings.Contains(d.Msg, "@timeout") || !strings.Contains(d.Msg, "event") {
		t.Errorf("msg = %q", d.Msg)
	}
}

// Members declared in an `extend service` block belong to the same
// service, so they merge into one ServiceInfo alongside the primary
// block's.
func TestExtendServiceMergesEventsAndConsumers(t *testing.T) {
	pkg := expectClean(t, `package p
type P { id string }
service S {
	event One { payload P }
}
extend service S {
	event Two { payload P }
	consume C { event One }
}`)
	si := pkg.Services["S"]
	if len(si.Events) != 2 {
		t.Errorf("merged events = %d, want 2", len(si.Events))
	}
	if len(si.Consumers) != 1 {
		t.Errorf("merged consumers = %d, want 1", len(si.Consumers))
	}
}

func TestEventNameCaseWarning(t *testing.T) {
	d := expectWarning(t, `package p
type P { id string }
service S {
	event lowered { payload P }
}`, CodeDeclNameCase)
	if !strings.Contains(d.Msg, "event name") {
		t.Errorf("msg = %q, want it to name the event site", d.Msg)
	}
}

// A consume scaffolds no file: it is a method on the handler interface
// the application implements where it likes, so it competes with nothing
// in the service logic folder - not a method of its own service, and not
// a method another service puts in a shared @group.
func TestConsumerNameCompetesWithNoFile(t *testing.T) {
	expectClean(t, `package p
type P { id string }
service S {
	get Foo /foo { response P }
	event E { payload P }
	consume Foo { event E }
}`)
	root, files := projectFixture(t, map[string]string{
		"a/a.craftgo": `package a
type P { id string }
@group("ops")
service A {
	event E { payload P }
	consume Handle { event E }
}
@group("ops")
service B {
	get Handle /h { response P }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeGroupMethodCollision); d != nil {
		t.Fatalf("unexpected %s: %s", CodeGroupMethodCollision, d.Msg)
	}
}

// Two services in one package may each name a consumer the same way as
// long as they consume different contracts: the table is keyed per
// service and their stubs land in different folders.
func TestConsumerNamesAreScopedPerService(t *testing.T) {
	const src = `package p
type P { id string }
service Producer {
	event E { payload P }
	event F { payload P }
}
service Mailer  { consume Handle { event E } }
service Auditor { consume Handle { event F } }`
	pkg := expectClean(t, src)
	if len(pkg.Consumers) != 2 {
		t.Fatalf("consumers = %d, want 2: %v", len(pkg.Consumers), pkg.Consumers)
	}
	proj, _ := AnalyzeProject(parseFiles(t, src), Options{})
	var services []string
	for _, c := range proj.Consumers() {
		services = append(services, c.Service)
	}
	if len(services) != 2 {
		t.Fatalf("resolved consumers = %v, want one per service", services)
	}
}

// A consumer's `event` clause names a contract, so events must be a
// declaration kind the lookup can yield - otherwise completion for
// `pkg.<cursor>` in that clause can offer everything except the one thing
// it accepts.
func TestEventsAreALookupKind(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"upstream/upstream.craftgo": `package upstream
type P { id string }
@contract("x.v1")
event PaymentSettled { payload P }
service S {
	event Shipped { payload P }
}`,
	})
	proj, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	pkg := proj.Packages["upstream"]
	if pkg == nil {
		t.Fatal("package missing")
	}
	var names []string
	for _, d := range pkg.Decls(EventDecls) {
		names = append(names, d.DeclName())
	}
	for _, want := range []string{"PaymentSettled", "Shipped"} {
		if !slices.Contains(names, want) {
			t.Errorf("Decls(EventDecls) missing %q: %v", want, names)
		}
	}
	// An event is a contract, never a type shape.
	for _, d := range pkg.Decls(TypeShapeDecls) {
		if d.DeclName() == "PaymentSettled" {
			t.Error("an event is offered in a type-shape position")
		}
	}
	if d := pkg.Decl("PaymentSettled", EventDecls); d == nil {
		t.Error("Decl cannot find a file-level event")
	}
}

// Consumer names are scoped to their service, so two services may reuse
// one.
func TestConsumerNameReusedAcrossServices(t *testing.T) {
	expectClean(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Audit { consume Process { event Placed } }
service Metrics { consume Process { event Placed } }`)
}

// One name may be reused on different contracts, and one service may
// consume a contract twice: which group each subscription joins is
// decided by the application, not here.
func TestConsumerNameReusedOnAnotherContract(t *testing.T) {
	expectClean(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
service A { consume Process { event Placed } }
service B { consume Process { event Cancelled } }`)
	expectClean(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Worker {
	consume Handle { event Placed }
	consume Audit { event Placed }
}`)
}

// A consumer declared in an `extend service` block belongs to the owning
// service, so it merges into that service's consumer set.
func TestExtendServiceConsumerBelongsToItsService(t *testing.T) {
	proj, diags := AnalyzeProject(parseFiles(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
service Watchers { consume Watch { event Placed } }
@group("ops")
extend service Watchers { consume Trail { event Cancelled } }`), Options{})
	expectNoDiags(t, diags)
	for _, c := range proj.Consumers() {
		if c.Service != "Watchers" {
			t.Errorf("%s belongs to %q, want Watchers", c.Name, c.Service)
		}
	}
}

// The decorators that named a broker group and a consume chain are gone.
// A design still carrying one is told what replaced it rather than that
// the name was never a decorator.
func TestRemovedEventDecoratorsAreRejectedWithTheirMigration(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "consumerGroup",
			src: `package p
type P { id string }
service Orders { event Placed { payload P } }
@consumerGroup("shared")
service Watchers { consume Watch { event Placed } }`,
			want: "RegisterOrdersHandler",
		},
		{
			name: "consumeMiddlewares",
			src: `package p
type P { id string }
service Orders { event Placed { payload P } }
service Watchers {
	@consumeMiddlewares(Retry)
	consume Watch { event Placed }
}`,
			want: "craftevents.Chain",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := expectDiag(t, c.src, CodeDecoratorRemoved)
			expectMessage(t, d, c.want)
		})
	}
}

// A design still carrying `@key` is told what replaced it. "Unknown
// decorator" would be true and useless: the author has to learn that the
// key moved to the publish call, and the diagnostic is where they look.
func TestAKeyDecoratorIsRejectedWithItsMigration(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service S {
	@key(id)
	event E { payload P }
}`, CodeDecoratorRemoved)
	for _, want := range []string{"@key", "no longer", "WithKey"} {
		if !strings.Contains(d.Msg, want) {
			t.Errorf("migration message does not mention %q: %s", want, d.Msg)
		}
	}
}

// A decorator that never existed keeps the message it had: the removed
// set is not a catch-all for typos.
func TestAnUnrelatedUnknownDecoratorIsStillUnknown(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service S {
	@keyy(id)
	event E { payload P }
}`, CodeDecoratorUnknown)
	if !strings.Contains(d.Msg, "unknown decorator @keyy") {
		t.Errorf("msg = %q", d.Msg)
	}
}
