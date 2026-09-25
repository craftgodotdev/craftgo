package golang

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/wire"
)

// methodMode is which blocks a method declares and which transport sides logic owns ([wire.RawSides]).
type methodMode struct {
	HasRequest  bool
	HasResponse bool
	RawRequest  bool
	RawResponse bool
}

// modeOf reads m's blocks and the raw-mode decorators among decs, those that apply to m.
func modeOf(m *ast.Method, decs []*ast.Decorator) methodMode {
	rawReq, rawResp := wire.RawSides(decs)
	return methodMode{
		HasRequest:  m.Request != nil,
		HasResponse: m.Response != nil && m.Response.Type != nil,
		RawRequest:  rawReq,
		RawResponse: rawResp,
	}
}

// BindRequest reports whether the handler binds and validates the request and logic receives it
// as `req *T`.
func (m methodMode) BindRequest() bool { return m.HasRequest && !m.RawRequest }

// WriteResponse reports whether the handler writes the response
// (headers, status, JSON body) after logic returns.
func (m methodMode) WriteResponse() bool { return !m.RawResponse }

// StubReturnsResp reports whether the service stub returns `(*T, error)`.
func (m methodMode) StubReturnsResp() bool { return m.HasResponse && !m.RawResponse }

// methodSignature is the Go shape of one logic entry point, shared by its declaration and its call.
type methodSignature struct {
	// Params is the parameter list without parens, e.g. `w http.ResponseWriter, r *http.Request, req *types.LoginReq`.
	Params string
	// Results is `(*types.LoginResp, error)` or the bare `error`.
	Results string
	// CallArgs is the argument list the caller passes: `w, r, &req`, `r`, `&req`, `w, r`, or "".
	CallArgs string
	// HasResult reports whether the call returns a response value (`resp, err := ...`).
	HasResult bool
	// NeedsHTTP reports whether the stub names net/http types (any raw side).
	NeedsHTTP bool
	// NeedsGRPC reports whether the stub names a grpc stream type.
	NeedsGRPC bool
}

// buildSignature derives the stub signature for mode from the rendered reqRef and respRef;
// the raw-side handles (`w, r` or `r`) precede `req`.
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
	if mode.BindRequest() {
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
