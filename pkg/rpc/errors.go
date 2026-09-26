package rpc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/pkg/server"
)

// UnknownErrorHandler turns an error [Error] cannot map into the status the
// client receives. The default logs it to [log.Default] and answers Internal
// with an opaque message.
type UnknownErrorHandler func(ctx context.Context, err error) error

var unknownError atomic.Pointer[UnknownErrorHandler]

func init() { SetHandleUnknownError(nil) }

func defaultUnknownError(ctx context.Context, err error) error {
	log.Default().WithContext(ctx).Error("unhandled service error", log.Err(err))
	return status.Error(codes.Internal, "internal server error")
}

// SetHandleUnknownError installs the process-wide [UnknownErrorHandler]; nil
// restores the default.
func SetHandleUnknownError(h UnknownErrorHandler) {
	if h == nil {
		h = defaultUnknownError
	}
	unknownError.Store(&h)
}

// Error maps err to a gRPC status error. A status error passes through; a
// [server.StatusError] keeps its text under its HTTP status's code, with an
// ErrorInfo carrying its ErrCode if any; a context error becomes Canceled or
// DeadlineExceeded; anything else goes to the installed [UnknownErrorHandler].
func Error(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	var se server.StatusError
	if errors.As(err, &se) {
		st := status.New(codeFor(se.HTTPStatus()), se.Error())
		var coded interface{ ErrCode() string }
		if errors.As(err, &coded) {
			info := &errdetails.ErrorInfo{Reason: coded.ErrCode(), Domain: serviceOf(ctx)}
			if detailed, derr := st.WithDetails(info); derr == nil {
				st = detailed
			}
		}
		return st.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	return (*unknownError.Load())(ctx, err)
}

// serviceOf returns the service of the call's full method
// (`/greet.Greeter/SayHello` → `greet.Greeter`), or "" outside a call.
func serviceOf(ctx context.Context) string {
	full, ok := grpc.Method(ctx)
	if !ok {
		return ""
	}
	full = strings.TrimPrefix(full, "/")
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[:i]
	}
	return full
}

// httpCodes maps each HTTP status the error catalogue can declare to the gRPC
// code of the same meaning; codeFor answers Unknown for any other status.
var httpCodes = map[int]codes.Code{
	http.StatusBadRequest:            codes.InvalidArgument,
	http.StatusUnauthorized:          codes.Unauthenticated,
	http.StatusPaymentRequired:       codes.FailedPrecondition,
	http.StatusForbidden:             codes.PermissionDenied,
	http.StatusNotFound:              codes.NotFound,
	http.StatusMethodNotAllowed:      codes.Unimplemented,
	http.StatusNotAcceptable:         codes.InvalidArgument,
	http.StatusRequestTimeout:        codes.DeadlineExceeded,
	http.StatusConflict:              codes.AlreadyExists,
	http.StatusGone:                  codes.NotFound,
	http.StatusLengthRequired:        codes.InvalidArgument,
	http.StatusPreconditionFailed:    codes.FailedPrecondition,
	http.StatusRequestEntityTooLarge: codes.ResourceExhausted,
	http.StatusUnsupportedMediaType:  codes.InvalidArgument,
	http.StatusUnprocessableEntity:   codes.InvalidArgument,
	http.StatusLocked:                codes.FailedPrecondition,
	http.StatusTooManyRequests:       codes.ResourceExhausted,
	http.StatusInternalServerError:   codes.Internal,
	http.StatusNotImplemented:        codes.Unimplemented,
	http.StatusBadGateway:            codes.Unavailable,
	http.StatusServiceUnavailable:    codes.Unavailable,
	http.StatusGatewayTimeout:        codes.DeadlineExceeded,
}

func codeFor(httpStatus int) codes.Code {
	if c, ok := httpCodes[httpStatus]; ok {
		return c
	}
	return codes.Unknown
}
