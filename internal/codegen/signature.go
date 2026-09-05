package codegen

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// methodMode is the per-method answer to "who owns each transport side":
// block presence from the DSL plus the raw flags read through
// wire.RawSides. Every codegen emitter (transport handler, service stub,
// OpenAPI operation, routes) derives its mode-dependent decisions from
// this one value so the layers cannot disagree.
type methodMode struct {
	HasRequest  bool
	HasResponse bool
	RawRequest  bool
	RawResponse bool
}

// modeOf reads m's blocks and raw-mode decorators.
func modeOf(m *ast.Method) methodMode {
	rawReq, rawResp := wire.RawSides(m.Decorators)
	return methodMode{
		HasRequest:  m.Request != nil,
		HasResponse: m.Response != nil && m.Response.Type != nil,
		RawRequest:  rawReq,
		RawResponse: rawResp,
	}
}

// BindRequest reports whether the handler binds + validates a request
// struct before calling logic: a declared request on a framework-owned
// request side. A request block on a raw side is docs-only.
func (m methodMode) BindRequest() bool { return m.HasRequest && !m.RawRequest }

// WriteResponse reports whether the handler writes the response
// (headers, status, JSON body) after logic returns.
func (m methodMode) WriteResponse() bool { return !m.RawResponse }

// StubTakesReq reports whether the service stub receives `req *T`.
func (m methodMode) StubTakesReq() bool { return m.HasRequest && !m.RawRequest }

// StubReturnsResp reports whether the service stub returns `(*T, error)`
// rather than a bare error.
func (m methodMode) StubReturnsResp() bool { return m.HasResponse && !m.RawResponse }

// methodSignature is the Go-side shape of one service entry point. The
// service template renders the declaration from Params / Results and the
// transport template renders the call from CallArgs, so both come from
// the same buildSignature call and cannot drift.
type methodSignature struct {
	// Params is the parameter list without parens, e.g.
	// `w http.ResponseWriter, r *http.Request, req *types.LoginReq`.
	Params string
	// Results is the result list as written after the parameters:
	// `(*types.LoginResp, error)` or the bare `error`.
	Results string
	// CallArgs is the argument list the transport passes: `w, r, &req`,
	// `r`, `&req`, `w, r`, or "".
	CallArgs string
	// HasResult reports whether the call yields a response value the
	// transport must encode (`resp, err := ...`).
	HasResult bool
	// NeedsHTTP reports whether the stub mentions net/http types (any raw
	// side) and therefore needs the import.
	NeedsHTTP bool
}

// buildSignature derives the stub signature for mode. reqRef / respRef
// are the rendered Go type references (`types.LoginReq`,
// `shared.Page[types.Order]`) and are only read for the sides the stub
// names. Parameter order is fixed: the raw-side handles first (`w, r`
// for a raw response, `r` alone for a raw request), then the bound
// request.
func buildSignature(mode methodMode, reqRef, respRef string) methodSignature {
	var params, args []string
	switch {
	case mode.RawResponse:
		params = append(params, "w http.ResponseWriter", "r *http.Request")
		args = append(args, "w", "r")
	case mode.RawRequest:
		params = append(params, "r *http.Request")
		args = append(args, "r")
	}
	if mode.StubTakesReq() {
		params = append(params, "req *"+reqRef)
		args = append(args, "&req")
	}
	sig := methodSignature{
		Params:    strings.Join(params, ", "),
		CallArgs:  strings.Join(args, ", "),
		Results:   "error",
		HasResult: mode.StubReturnsResp(),
		NeedsHTTP: mode.RawRequest || mode.RawResponse,
	}
	if sig.HasResult {
		sig.Results = "(*" + respRef + ", error)"
	}
	return sig
}
