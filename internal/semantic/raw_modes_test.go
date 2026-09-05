package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// ---------- raw modes: @rawRequest / @rawResponse / @passthrough ----------

// Every cell of the request × response matrix analyses clean, with and
// without blocks: a block on a raw side is a docs-only contract, not an
// error.
func TestRawModesAnalyseClean(t *testing.T) {
	mustClean(t, `package x
type Req { id string @path  q string? @query }
type Resp { ok bool }
service S {
    @rawResponse
    get A /a/{id} { request Req  response Resp }
    @rawResponse
    get B /b {}
    @rawRequest
    post C /c { response Resp }
    @rawRequest
    post D /d {}
    @passthrough
    get E /e/{id} { request Req  response Resp }
    @passthrough
    get F /f {}
}`)
}

func TestRawModeRedundancyFlagAfterPassthrough(t *testing.T) {
	d := expectWarning(t, `package x
service S {
    @passthrough
    @rawRequest
    get A /a {}
}`, CodeDecoratorRedundant)
	expectMessage(t, d, "@rawRequest is redundant on method S.A", "request side raw")
	if d.Pos.Line != 4 {
		t.Errorf("warning must anchor on the later decorator (line 4), got line %d", d.Pos.Line)
	}
	if len(d.Related) != 1 || d.Related[0].Pos.Line != 3 {
		t.Errorf("related must point at @passthrough on line 3, got %+v", d.Related)
	}
}

func TestRawModeRedundancyPassthroughAfterFlag(t *testing.T) {
	d := expectWarning(t, `package x
service S {
    @rawResponse
    @passthrough
    get A /a {}
}`, CodeDecoratorRedundant)
	expectMessage(t, d, "@passthrough on method S.A already covers @rawResponse")
	if d.Pos.Line != 4 {
		t.Errorf("warning must anchor on the later decorator (line 4), got line %d", d.Pos.Line)
	}
	if len(d.Related) != 1 || d.Related[0].Pos.Line != 3 {
		t.Errorf("related must point at @rawResponse on line 3, got %+v", d.Related)
	}
}

func TestRawModeRedundancyBothFlags(t *testing.T) {
	d := expectWarning(t, `package x
service S {
    @rawRequest
    @rawResponse
    get A /a {}
}`, CodeDecoratorRedundant)
	expectMessage(t, d, "exactly @passthrough", "write @passthrough instead")
	if d.Pos.Line != 4 {
		t.Errorf("warning must anchor on the later decorator (line 4), got line %d", d.Pos.Line)
	}
	expectCodeCount(t, `package x
service S {
    @rawRequest
    @rawResponse
    get A /a {}
}`, CodeDecoratorRedundant, 1)
}

// A `@passthrough` on an `extend service` header is cloned onto every
// method before the method's own decorators, so a method-level flag is
// the later spelling and carries the warning.
func TestRawModeRedundancyPropagatedFromExtend(t *testing.T) {
	d := expectWarning(t, `package x
service S {
    get A /a {}
}
@passthrough
extend service S {
    @rawResponse
    get B /b {}
}`, CodeDecoratorRedundant)
	expectMessage(t, d, "@rawResponse is redundant on method S.B")
	if d.Pos.Line != 7 {
		t.Errorf("warning must anchor on the method's own flag (line 7), got line %d", d.Pos.Line)
	}
}

// A raw-request method reads path values off the *http.Request, so the
// path/param-missing warning is suppressed only when there is no request
// block; a declared block must still cover every path segment.
func TestRawRequestPathParamNoBlockIsClean(t *testing.T) {
	mustClean(t, `package x
service S {
    @rawRequest
    get A /users/{id} {}
}`)
}

func TestRawRequestPathParamBlockMustCoverSegment(t *testing.T) {
	d := expectDiag(t, `package x
type Req { q string? @query }
service S {
    @rawRequest
    get A /users/{id} { request Req }
}`, CodePathParamMissing)
	expectMessage(t, d, "{id} has no matching field")
}

// @rawResponse alone leaves the request side framework-bound, so a path
// segment with no request struct still warns.
func TestRawResponsePathParamStillWarns(t *testing.T) {
	expectDiag(t, `package x
service S {
    @rawResponse
    get A /users/{id} {}
}`, CodePathParamMissing)
}

// The docs-only response block still obeys the status rules: a
// no-content status cannot advertise a body.
func TestRawResponseNoContentStatusConflict(t *testing.T) {
	expectError(t, `package x
type Resp { ok bool }
service S {
    @rawResponse
    @status(204)
    get A /a { response Resp }
}`, CodeDecoratorConflict)
}

func TestRawModeFlagsRejectParens(t *testing.T) {
	expectWarning(t, `package x
service S {
    @rawRequest()
    get A /a {}
}`, CodeFlagEmptyParens)
}

func TestRawModeFlagsMethodLevelOnly(t *testing.T) {
	d := expectDiag(t, `package x
type X { body string @rawResponse }`, CodeDecoratorPlacement)
	expectMessage(t, d, "@rawResponse is not allowed on field")
	d = expectDiag(t, `package x
@rawRequest
service S {}`, CodeDecoratorPlacement)
	expectMessage(t, d, "@rawRequest is not allowed on service")
}

// All three raw-mode decorators on one method are accepted in any order:
// only redundancy warnings fire (one per superfluous decorator), never an
// error, and the method still analyses like a plain @passthrough.
func TestRawModeAllThreeFlagsWarnOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decs     string
		warnings int
	}{
		{"passthrough first", "@passthrough\n    @rawRequest\n    @rawResponse", 2},
		{"passthrough last", "@rawRequest\n    @rawResponse\n    @passthrough", 3},
		{"passthrough middle", "@rawResponse\n    @passthrough\n    @rawRequest", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package x\ntype Req { id string @path }\ntype Resp { ok bool }\nservice S {\n    " + tc.decs + "\n    get A /a/{id} { request Req  response Resp }\n}"
			_, diags := Analyze(parseFiles(t, src))
			for _, d := range diags {
				if d.Severity == lexer.SeverityError {
					t.Errorf("unexpected error diagnostic: %v", d)
				}
			}
			expectCodeCount(t, src, CodeDecoratorRedundant, tc.warnings)
		})
	}
}

// Raw modes combine with every other method-level decorator without a
// diagnostic (the only raw-specific rule is the redundancy warning).
func TestRawModesMixWithMethodDecoratorsClean(t *testing.T) {
	mustClean(t, `package x
middleware Auth
middleware Audit
error NotFound ThingMissing { id string }
type Req { id string @path @length(1, 32) }
type Resp { ok bool  trace string @header("X-Trace") }
type Body { name string @minLength(1) }
@prefix("/mix")
@tags(mix)
@security(Bearer)
@middlewares(Auth)
service S {
    @doc("everything")
    @summary("RR")
    @operationId("rrAll")
    @tags(extra)
    @security(Admin)
    @errors(ThingMissing)
    @status(202)
    @timeout(3s)
    @maxBodySize(2MB)
    @middlewares(Audit)
    @deprecated("use other")
    @rawResponse
    post RrAll /rr/{id} { request Req  response Resp }
    @ignoreMiddleware
    @ignoreSecurity
    @ignoreTags
    @rawRequest
    post RqIgnore /rq { request Body  response Resp }
    @errors(ThingMissing)
    @status(202)
    @timeout(3s)
    @passthrough
    post PtAll /pt/{id} { request Req  response Resp }
}`)
}
