package golang

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// grpcSignature is the Go-side shape of one RPC, for the server layer
// and the logic scaffold. Both render from the same value, so the
// server's call and the logic's declaration cannot drift - the rule
// [buildSignature] sets for HTTP. The server method must match the
// interface protoc-gen-go-grpc generates; the logic drops the context
// the scaffold already carries.
type grpcSignature struct {
	// Kind names the streaming shape for the generated comments.
	Kind string
	// IsUnary reports a plain request/response call, the one shape whose
	// server method receives a context parameter.
	IsUnary bool
	// HasRequest reports that the call starts with one request message
	// (unary and server-streaming), which the server layer validates.
	HasRequest bool
	// HasResponse reports that logic returns a response value (unary).
	HasResponse bool
	// ServerParams is the server method's parameter list, matching the
	// generated interface.
	ServerParams string
	// Logic is the logic scaffold's signature; its CallArgs are what the
	// server passes.
	Logic methodSignature
}

// buildGRPCSignature derives the signatures of m. in and out are the
// rendered request and response types (`pb.HelloRequest`).
func buildGRPCSignature(m *protodesign.Method, in, out string) grpcSignature {
	sig := grpcSignature{Kind: m.Kind.String()}
	var serverParams, params, args []string
	switch m.Kind {
	case protodesign.Unary:
		sig.IsUnary, sig.HasRequest, sig.HasResponse = true, true, true
		serverParams = []string{"ctx context.Context", "req *" + in}
		params = []string{"req *" + in}
		args = []string{"req"}
	case protodesign.ServerStream:
		sig.HasRequest = true
		stream := "stream grpc.ServerStreamingServer[" + out + "]"
		serverParams = []string{"req *" + in, stream}
		params = []string{"req *" + in, stream}
		args = []string{"req", "stream"}
	case protodesign.ClientStream:
		stream := "stream grpc.ClientStreamingServer[" + in + ", " + out + "]"
		serverParams = []string{stream}
		params = []string{stream}
		args = []string{"stream"}
	case protodesign.Bidi:
		stream := "stream grpc.BidiStreamingServer[" + in + ", " + out + "]"
		serverParams = []string{stream}
		params = []string{stream}
		args = []string{"stream"}
	}
	sig.ServerParams = strings.Join(serverParams, ", ")
	sig.Logic = methodSignature{
		Params:    strings.Join(params, ", "),
		CallArgs:  strings.Join(args, ", "),
		Results:   "error",
		HasResult: sig.HasResponse,
		NeedsGRPC: !sig.IsUnary,
	}
	if sig.HasResponse {
		sig.Logic.Results = "(*" + out + ", error)"
	}
	return sig
}

// streamNotes is the usage hint the logic scaffold carries for a
// streaming RPC, one line per entry.
func streamNotes(kind protodesign.Kind) []string {
	switch kind {
	case protodesign.ServerStream:
		return []string{"Send each response with stream.Send; returning ends the stream."}
	case protodesign.ClientStream:
		return []string{"Read requests with stream.Recv until io.EOF, then answer with", "stream.SendAndClose."}
	case protodesign.Bidi:
		return []string{"Read requests with stream.Recv and answer with stream.Send;", "returning ends the stream."}
	}
	return nil
}
