package semantic

import (
	"strings"
	"testing"
)

func TestOperationIDDuplicateExplicitRejected(t *testing.T) {
	// Two methods pinned to the same explicit @operationId collide - the spec
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

// Two methods in DIFFERENT packages with the same explicit @operationId clash in
// the merged OpenAPI document; the project pass reports it with a position the
// per-package pass (one package at a time) can't.
func TestProjectOperationIDCrossPkgExplicitDup(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"alpha/a.craftgo": `package alpha
type AResp { ok bool }
service AlphaService {
	@operationId("getThing")
	get Foo /foo { response AResp }
}`,
		"beta/b.craftgo": `package beta
type BResp { ok bool }
service BetaService {
	@operationId("getThing")
	get Bar /bar { response BResp }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDuplicateOperation) == nil {
		t.Errorf("cross-package explicit operationId dup should be flagged; got %v", codes(diags))
	}
}

// Auto ids that share a method name across packages are service-prefixed in the
// merged document, so they do NOT clash - no false positive.
func TestProjectOperationIDCrossPkgAutoNoFalsePositive(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"alpha/a.craftgo": `package alpha
type AResp { ok bool }
service AlphaService { get GetUser /a/u { response AResp } }`,
		"beta/b.craftgo": `package beta
type BResp { ok bool }
service BetaService { get GetUser /b/u { response BResp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDuplicateOperation); d != nil {
		t.Errorf("auto ids are service-prefixed in the merged doc; must not collide: %s", d.Msg)
	}
}
