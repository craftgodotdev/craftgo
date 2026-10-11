package rpc

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Validate calls msg's `Validate() error`, as protoc-gen-validate generates it,
// and returns a failure as InvalidArgument; a message without one passes.
func Validate(msg any) error {
	v, ok := msg.(interface{ Validate() error })
	if !ok {
		return nil
	}
	if err := v.Validate(); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

// ValidateClientStream returns stream with each request its Recv receives run
// through [Validate]: a message that fails is consumed, and Recv returns the
// InvalidArgument error.
func ValidateClientStream[Req, Res any](stream grpc.ClientStreamingServer[Req, Res]) grpc.ClientStreamingServer[Req, Res] {
	return &grpc.GenericServerStream[Req, Res]{ServerStream: validatingStream{stream}}
}

// ValidateBidiStream is [ValidateClientStream] for a bidi-streaming RPC.
func ValidateBidiStream[Req, Res any](stream grpc.BidiStreamingServer[Req, Res]) grpc.BidiStreamingServer[Req, Res] {
	return &grpc.GenericServerStream[Req, Res]{ServerStream: validatingStream{stream}}
}

// validatingStream runs [Validate] on every message it receives.
type validatingStream struct{ grpc.ServerStream }

func (s validatingStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	return Validate(m)
}
