package rpc

import (
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
