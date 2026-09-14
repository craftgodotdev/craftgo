package semantic

import "testing"

// design wraps a case in the events scaffolding it needs: a payload type,
// a producing service and a consuming one. decls go at file level and
// consumerDecs directly above the `consume` member.
func design(decls, consumerDecs string) string {
	return `package p
type P { id string }
` + decls + `
service Orders {
	event Placed { payload P }
}
service Watchers {` + consumerDecs + `
	consume Watch {
		event Placed
	}
}`
}

func TestConsumeMiddlewareDeclares(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, design("consume middleware Retry", "")))
	if len(diags) != 0 {
		t.Fatalf("clean design diagnosed: %v", diags)
	}
	if pkg.ConsumeMiddlewares["Retry"] == nil {
		t.Fatal("Retry is not in the consume table")
	}
	if pkg.Middlewares["Retry"] != nil {
		t.Error("Retry leaked into the HTTP table")
	}
	if !pkg.ConsumeMiddlewares["Retry"].Consume {
		t.Error("the decl is not marked Consume")
	}
}

func TestConsumeMiddlewareApplies(t *testing.T) {
	_, diags := Analyze(parseFiles(t, design(
		"consume middleware Retry", "\n\t@consumeMiddlewares(Retry)")))
	if len(diags) != 0 {
		t.Fatalf("@consumeMiddlewares on a consumer diagnosed: %v", diags)
	}
}

func TestConsumeMiddlewareAppliesAtServiceLevel(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package p
type P { id string }
consume middleware Retry
service Orders { event Placed { payload P } }
@consumeMiddlewares(Retry)
service Watchers { consume Watch { event Placed } }`))
	if len(diags) != 0 {
		t.Fatalf("service-level @consumeMiddlewares diagnosed: %v", diags)
	}
}

// A name declared on one side and used on the other is the wrong FORM,
// not a typo, so it reports the kind rather than "not declared".
func TestHTTPMiddlewareOnConsumerIsKindMismatch(t *testing.T) {
	d := expectDiag(t, design("middleware Retry", "\n\t@consumeMiddlewares(Retry)"),
		CodeMiddlewareKindMismatch)
	expectMessage(t, d,
		"is declared `middleware Retry`",
		"func(http.Handler) http.Handler",
		"consume middleware Retry",
		"func(events.Subscription, events.Handler) events.Handler")
}

func TestConsumeMiddlewareOnMethodIsKindMismatch(t *testing.T) {
	d := expectDiag(t, `package p
consume middleware Retry
type Pong { ok bool }
service S {
	@middlewares(Retry)
	get Ping / { response Pong }
}`, CodeMiddlewareKindMismatch)
	expectMessage(t, d, "is declared `consume middleware Retry`",
		"func(events.Subscription, events.Handler) events.Handler")
}

func TestUnknownConsumeMiddlewareIsRefError(t *testing.T) {
	expectDiag(t, design("", "\n\t@consumeMiddlewares(Nope)"), CodeDecoratorRef)
}

// One name is one middleware of one kind, whichever table it lands in.
func TestConsumeAndHTTPMiddlewareCannotShareAName(t *testing.T) {
	expectDiag(t, `package p
middleware Auth
consume middleware Auth
service S {}`, CodeDuplicateDecl)
}

func TestConsumeMiddlewareCollidesAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a.craftgo": "package a\nmiddleware Auth",
		"b.craftgo": "package b\nconsume middleware Auth",
	})
	_ = root
	_, diags := AnalyzeProject(files, Options{})
	if findCode(diags, CodeMiddlewareCollision) == nil {
		t.Fatalf("cross-package cross-kind collision not reported: %v", codes(diags))
	}
}

func TestIgnoreMiddlewareOnConsumer(t *testing.T) {
	_, diags := Analyze(parseFiles(t, design(
		"consume middleware Retry", "\n\t@ignoreMiddleware\n\t@consumeMiddlewares(Retry)")))
	if len(diags) != 0 {
		t.Fatalf("@ignoreMiddleware on a consumer diagnosed: %v", diags)
	}
}

func TestHTTPMiddlewareStillRejectedOnConsumer(t *testing.T) {
	// @middlewares has no consumer level, so this stays a placement
	// error - and only one, not a placement plus a resolution complaint.
	_, diags := Analyze(parseFiles(t, design("middleware Auth", "\n\t@middlewares(Auth)")))
	if findCode(diags, CodeDecoratorPlacement) == nil {
		t.Fatalf("expected a placement diagnostic, got %v", codes(diags))
	}
	if len(diags) != 1 {
		t.Errorf("one mistake should give one diagnostic, got %v", codes(diags))
	}
}

// The extend block's chain reaches that block's consumers, the way it
// already reaches its methods.
func TestConsumeMiddlewarePropagatesFromExtendBlock(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, `package p
type P { id string }
consume middleware Retry
service Orders { event Placed { payload P } }
service Watchers {}
@consumeMiddlewares(Retry)
extend service Watchers {
	consume Watch { event Placed }
}`))
	if len(diags) != 0 {
		t.Fatalf("diagnosed: %v", diags)
	}
	cs := pkg.Services["Watchers"].Consumers
	if len(cs) != 1 {
		t.Fatalf("want 1 consumer, got %d", len(cs))
	}
	found := false
	for _, d := range cs[0].Decorators {
		if d.Name == "consumeMiddlewares" && d.Propagated {
			found = true
		}
	}
	if !found {
		t.Errorf("the block's chain did not reach its consumer: %v", cs[0].Decorators)
	}
}

// The level set is the contract every other pass reads, so widening one
// by a line is a change this notices.
func TestMiddlewareDecoratorLevels(t *testing.T) {
	for name, want := range map[string]Level{
		"middlewares":        LvlService | LvlMethod,
		"consumeMiddlewares": LvlService | LvlConsumer,
		"ignoreMiddleware":   LvlMethod | LvlConsumer,
	} {
		spec, ok := Lookup(name)
		if !ok {
			t.Errorf("@%s is not registered", name)
			continue
		}
		if spec.Levels != want {
			t.Errorf("@%s levels = %v, want %v", name, spec.Levels, want)
		}
	}
}

// The fan-out is not limited to @consumeMiddlewares: an extend block's
// decorators reach the members whose level they fit, so @doc and
// @deprecated land on its consumers the way they already land on its
// methods. Both are LvlConsumer, and before the fan-out neither reached a
// consumer at all.
func TestExtendBlockDocAndDeprecatedReachConsumers(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, `package p
type P { id string }
service Orders { event Placed { payload P } }
service Watchers {}
@doc("Inherited from the block.")
@deprecated
extend service Watchers {
	consume Watch { event Placed }
}`))
	if len(diags) != 0 {
		t.Fatalf("diagnosed: %v", diags)
	}
	cs := pkg.Services["Watchers"].Consumers
	if len(cs) != 1 {
		t.Fatalf("want 1 consumer, got %d", len(cs))
	}
	got := map[string]bool{}
	for _, d := range cs[0].Decorators {
		if d.Propagated {
			got[d.Name] = true
		}
	}
	for _, want := range []string{"doc", "deprecated"} {
		if !got[want] {
			t.Errorf("@%s did not reach the block's consumer: %v", want, cs[0].Decorators)
		}
	}
}

// A decorator valid at neither member level is still refused on a block,
// and the message names both sites rather than only methods.
func TestExtendBlockRejectsAServiceOnlyDecorator(t *testing.T) {
	d := expectDiag(t, `package p
type Pong { ok bool }
service S {}
@prefix("/x")
extend service S { get Op /x { response Pong } }`, CodeExtendDecoratorNotMethod)
	expectMessage(t, d, "is not valid on a method or a consumer")
}
