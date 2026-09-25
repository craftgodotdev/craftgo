package golang

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/protodesign"
)

// grpcSignature is the Go shape of one RPC: ServerParams match the interface protoc-gen-go-grpc
// generates, and Logic drops the context the logic struct already carries.
type grpcSignature struct {
	// Kind names the streaming shape for the generated comments.
	Kind string
	// IsUnary is the one shape whose server method takes a context parameter.
	IsUnary bool
	// HasRequest reports a leading request message (unary, server-streaming), which the server validates.
	HasRequest bool
	// HasResponse reports that logic returns a response value (unary).
	HasResponse  bool
	ServerParams string
	// Logic is the logic scaffold's signature; the server calls it with Logic.CallArgs.
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

// grpcEntry is the generated doc line of m's logic entry point, a method of proto service svc:
// how a streaming RPC uses its stream.
func grpcEntry(svc string, m *protodesign.Method) string {
	switch m.Kind {
	case protodesign.ServerStream:
		return m.Name + " sends each response with stream.Send; returning ends the stream."
	case protodesign.ClientStream:
		return m.Name + " reads requests with stream.Recv until io.EOF, then answers with stream.SendAndClose."
	case protodesign.Bidi:
		return m.Name + " reads requests with stream.Recv and answers with stream.Send; returning ends the stream."
	}
	return m.Name + " implements " + svc + "." + m.Name + "."
}
