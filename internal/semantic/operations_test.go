package semantic

import (
	"strings"
	"testing"
)

func TestOperationIDDuplicateExplicitRejected(t *testing.T) {
	// Two methods pinned to the same explicit @operationId collide — the spec
	// would carry a duplicate operationId.
	_, diags := Analyze(parseFiles(t, `service S {
	@operationId("doThing")
	get A /a {}
	@operationId("doThing")
	get B /b {}
}`))
	d := findCode(diags, CodeDuplicateOperation)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "doThing") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestOperationIDOverrideEqualsAutoRejected(t *testing.T) {
	// An override equal to another method's auto operationId also collides.
	_, diags := Analyze(parseFiles(t, `service S {
	get ListUsers /u {}
	@operationId("ListUsers")
	get Other /o {}
}`))
	if findCode(diags, CodeDuplicateOperation) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestOperationIDUniqueClean(t *testing.T) {
	mustClean(t, `service S {
	get A /a {}
	@operationId("custom")
	get B /b {}
}`)
}

func TestOperationBaseNameServicePrefixesShared(t *testing.T) {
	// A method name shared by two services is service-prefixed so the bases
	// (and thus operationIds / component names) stay unique without an
	// explicit override.
	pkg, diags := Analyze(parseFiles(t, `service A { get List /a {} }
service B { get List /b {} }`))
	if d := findCode(diags, CodeDuplicateOperation); d != nil {
		t.Fatalf("auto-prefixing should avoid a collision, got %q", d.Msg)
	}
	counts := MethodNameCounts(pkg)
	if got := OperationBaseName("A", pkg.Services["A"].Methods[0], counts); got != "AList" {
		t.Errorf("shared method name should be service-prefixed, got %q", got)
	}
}
