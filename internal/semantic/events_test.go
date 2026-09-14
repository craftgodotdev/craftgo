package semantic

import (
	"fmt"
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
			name: "duplicate consumer of one contract",
			src: `package p
type P { id string }
service S {
	event E { payload P }
	consume A { event E }
	consume B { event E }
}`,
			code: CodeConsumerDuplicate,
			msg:  "already consumes",
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

// A consumer and a method of one service both scaffold `<name>.go` into
// the service logic folder, so sharing a name is rejected rather than
// silently dropping the second stub.
func TestConsumerCannotShareAMethodName(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service S {
	get Foo /foo { response P }
	event E { payload P }
	consume Foo { event E }
}`, CodeConsumerCollision)
	if !strings.Contains(d.Msg, "foo.go") {
		t.Errorf("msg should name the file both would claim, got %q", d.Msg)
	}
}

// The consumer handler set is one file per service at the root of the
// transport output, so two service names that fold to one file name under
// the configured file case are rejected rather than losing one service's
// consumers to the other's write.
func TestConsumerHandlerFileCollision(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/a.craftgo": `package a
type P { id string }
service Producer {
	event E { payload P }
	event F { payload P }
}
service UserAPI {
	consume One { event E }
}
service UserApi {
	consume Two { event F }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeConsumerHandlerCollision)
	if d == nil {
		t.Fatalf("want %s, got %v", CodeConsumerHandlerCollision, codes(diags))
	}
	if !strings.Contains(d.Msg, "user_api_consumers.go") {
		t.Errorf("msg should name the file both claim, got %q", d.Msg)
	}
	if len(d.Related) == 0 {
		t.Errorf("diagnostic should point at the other claimant")
	}
}

// A service whose name does not fold onto another's is left alone, and a
// service with no consumers claims no handler file at all.
func TestConsumerHandlerFileCollisionNeedsConsumersOnBoth(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/a.craftgo": `package a
type P { id string }
service Producer {
	event E { payload P }
}
service UserAPI {
	consume One { event E }
}
service UserApi {
	get Ping /ping { response P }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeConsumerHandlerCollision); d != nil {
		t.Fatalf("unexpected %s: %s", CodeConsumerHandlerCollision, d.Msg)
	}
}

// Two services sharing a @group share one logic folder, so a consumer
// name colliding with another service's member is the same error a
// colliding method name already was.
func TestGroupedConsumerCollidesAcrossServices(t *testing.T) {
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
	if findCode(diags, CodeGroupMethodCollision) == nil {
		t.Fatalf("want %s, got %v", CodeGroupMethodCollision, codes(diags))
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

// A group holds one position in the stream, so two consumers of one
// contract that resolve to the same group split it instead of each
// receiving every message.
func TestConsumerGroupCollisionOnOneContract(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
@consumerGroup("shared")
service Audit { consume Process { event Placed } }
@consumerGroup("shared")
service Metrics { consume Record { event Placed } }`, CodeConsumerGroupCollision)
	if !strings.Contains(d.Msg, `consumer group "shared" reads contract "p.Placed" more than once`) {
		t.Errorf("msg = %q", d.Msg)
	}
	if len(d.Related) != 1 {
		t.Fatalf("want the other claimant linked, got %v", d.Related)
	}
}

func TestConsumerGroupCollisionAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"orders/orders.craftgo": `package orders
type P { id string }
service OrderService { event Placed { payload P } }`,
		"audit/audit.craftgo": `package audit
service Audit { @consumerGroup("shared") consume Process { event orders.Placed } }`,
		"metrics/metrics.craftgo": `package metrics
service Metrics { @consumerGroup("shared") consume Record { event orders.Placed } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeConsumerGroupCollision) == nil {
		t.Fatalf("want %s, got %v", CodeConsumerGroupCollision, codes(diags))
	}
}

// The derived default qualifies the consumer name with its package and
// service, so two services reusing one consumer name no longer collide.
func TestConsumerNameReusedAcrossServices(t *testing.T) {
	expectClean(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Audit { consume Process { event Placed } }
service Metrics { consume Process { event Placed } }`)
}

// One name may be reused on different contracts: the group is the pair.
func TestConsumerNameReusedOnAnotherContract(t *testing.T) {
	expectClean(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
service A { consume Process { event Placed } }
service B { consume Process { event Cancelled } }`)
}

// The feature: one group spanning several contracts inside one service,
// so the group is the unit of scaling and of failure isolation.
func TestConsumerGroupSpansContractsInOneService(t *testing.T) {
	expectClean(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
@consumerGroup("orders-worker")
service Worker {
	consume OnPlaced { event Placed }
	consume OnCancelled { event Cancelled }
}`)
}

// A shared group requires every process joining it to register the same
// consumers; two services deploy as separate binaries and cannot.
func TestConsumerGroupAcrossServices(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
@consumerGroup("shared")
service A { consume OnPlaced { event Placed } }
@consumerGroup("shared")
service B { consume OnCancelled { event Cancelled } }`, CodeConsumerGroupCrossService)
	if !strings.Contains(d.Msg, `consumer group "shared" is claimed by more than one service`) {
		t.Errorf("msg = %q", d.Msg)
	}
	if len(d.Related) != 1 {
		t.Fatalf("want the other claimant linked, got %v", d.Related)
	}
}

// Precedence: the consumer's own decorator wins over its service's, and
// both win over the derived default.
func TestConsumerGroupPrecedence(t *testing.T) {
	proj, diags := AnalyzeProject(parseFiles(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
@consumerGroup("service-level")
service Worker {
	@consumerGroup("consumer-level")
	consume OnPlaced { event Placed }
	consume OnCancelled { event Cancelled }
}
service Plain { consume Watch { event Placed } }`), Options{})
	expectNoDiags(t, diags)
	want := map[string]string{
		"OnPlaced":    "consumer-level",
		"OnCancelled": "service-level",
		"Watch":       "p-Plain-Watch",
	}
	for _, c := range proj.Consumers() {
		if got := c.Group; got != want[c.Name] {
			t.Errorf("%s group = %q, want %q", c.Name, got, want[c.Name])
		}
	}
}

// `@group` decides where generated files land. It must not reach the
// broker identity: the same service grouped and ungrouped derives one
// group name.
func TestOutputGroupDoesNotReachConsumerGroup(t *testing.T) {
	const design = `package p
type P { id string }
service Orders { event Placed { payload P } }
%sservice Watchers { consume Watch { event Placed } }`
	groupOf := func(t *testing.T, src string) string {
		t.Helper()
		proj, diags := AnalyzeProject(parseFiles(t, src), Options{})
		expectNoDiags(t, diags)
		cs := proj.Consumers()
		if len(cs) != 1 {
			t.Fatalf("want one consumer, got %d", len(cs))
		}
		return cs[0].Group
	}
	plain := groupOf(t, fmt.Sprintf(design, ""))
	grouped := groupOf(t, fmt.Sprintf(design, "@group(\"ops\")\n"))
	if plain != grouped {
		t.Errorf("@group changed the consumer group: %q vs %q", plain, grouped)
	}
	if plain != "p-Watchers-Watch" {
		t.Errorf("derived group = %q, want %q", plain, "p-Watchers-Watch")
	}
}

// A consumer declared in an `extend service` block belongs to the owning
// service, so it derives that service's name and the extend block's
// @group contributes nothing.
func TestExtendServiceConsumerGroup(t *testing.T) {
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
	want := map[string]string{"Watch": "p-Watchers-Watch", "Trail": "p-Watchers-Trail"}
	for _, c := range proj.Consumers() {
		if got := c.Group; got != want[c.Name] {
			t.Errorf("%s group = %q, want %q", c.Name, got, want[c.Name])
		}
	}
}

// The service-level default reaches a consumer declared in an extend
// block: the decorator sits on the service that owns it.
func TestExtendServiceConsumerInheritsServiceGroup(t *testing.T) {
	proj, diags := AnalyzeProject(parseFiles(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
@consumerGroup("watch-worker")
service Watchers { consume Watch { event Placed } }
extend service Watchers { consume Trail { event Cancelled } }`), Options{})
	expectNoDiags(t, diags)
	for _, c := range proj.Consumers() {
		if c.Group != "watch-worker" {
			t.Errorf("%s group = %q, want the service default", c.Name, c.Group)
		}
	}
}

// An authored name that lands on another service's derived one is the
// cross-service case, caught the same way.
func TestAuthoredGroupCannotTakeAnotherServicesDerivedName(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
service Watchers { consume Watch { event Placed } }
@consumerGroup("p-Watchers-Watch")
service Trailers { consume Trail { event Cancelled } }`, CodeConsumerGroupCrossService)
	expectMessage(t, d, `consumer group "p-Watchers-Watch" is claimed by more than one service`)
	if len(d.Related) != 1 {
		t.Fatalf("want the other claimant linked, got %v", d.Related)
	}
}

// `@consumerGroup` is a service-level decorator, so an extend block
// carries the same rule `@prefix` does: it belongs on the primary
// declaration, which is the one that owns the consumers.
func TestConsumerGroupOnExtendBlockIsRejected(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service Orders {
	event Placed { payload P }
	event Cancelled { payload P }
}
service Watchers { consume Watch { event Placed } }
@consumerGroup("ops")
extend service Watchers { consume Trail { event Cancelled } }`, CodeExtendDecoratorNotMethod)
	expectMessage(t, d, "@consumerGroup on extend service")
}

func TestConsumerGroupWithDotIsRejected(t *testing.T) {
	d := expectDiag(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Watchers { @consumerGroup("orders.watch") consume Watch { event Placed } }`, CodeConsumerGroupFormat)
	if !strings.Contains(d.Msg, "JetStream") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestConsumerGroupWithSpaceIsRejected(t *testing.T) {
	expectDiag(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Watchers { @consumerGroup("orders watch") consume Watch { event Placed } }`, CodeConsumerGroupFormat)
}

// JetStream's checkConsumerName refuses these too, so a design carrying
// one compiles and then fails at the broker.
func TestConsumerGroupWithASubjectWildcardIsRejected(t *testing.T) {
	for _, name := range []string{"orders>watch", "orders*watch", "orders/watch", `orders\\watch`} {
		t.Run(name, func(t *testing.T) {
			expectDiag(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Watchers { @consumerGroup("`+name+`") consume Watch { event Placed } }`, CodeConsumerGroupFormat)
		})
	}
}

func TestConsumerGroupEmptyIsRejected(t *testing.T) {
	expectDiag(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Watchers { @consumerGroup("") consume Watch { event Placed } }`, CodeConsumerGroupFormat)
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
