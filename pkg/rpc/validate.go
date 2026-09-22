package rpc

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Validate runs the request message's own `Validate() error` - the method
// protoc-gen-validate generates - and turns a failure into
// InvalidArgument, the way the HTTP handler answers 400 from the generated
// Validate. A message without the method passes; a validator without one
// (protovalidate) is installed as an interceptor with Use.
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
