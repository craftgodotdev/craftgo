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

// UnknownErrorHandler turns an error that is neither a gRPC status nor a
// [server.StatusError] into the status the client receives. The default
// logs the full error with the call's trace ids and answers Internal with
// an opaque message - the raw text stays in the log, never on the wire.
type UnknownErrorHandler func(ctx context.Context, err error) error

var unknownError atomic.Value

func init() { unknownError.Store(UnknownErrorHandler(defaultUnknownError)) }

func defaultUnknownError(ctx context.Context, err error) error {
	log.Default().WithContext(ctx).Error("unhandled service error", log.Err(err))
	return status.Error(codes.Internal, "internal server error")
}

// SetHandleUnknownError installs a process-wide handler for service errors
// that carry neither a gRPC status nor an HTTP status. Pass nil to revert
// to the default.
func SetHandleUnknownError(h UnknownErrorHandler) {
	if h == nil {
		h = defaultUnknownError
	}
	unknownError.Store(h)
}

// Error is what the generated server layer returns when service logic
// fails, the counterpart of [server.WriteError]:
//
//   - a gRPC status error, or an error wrapping one, is returned as is;
//   - a [server.StatusError] anywhere in the chain becomes the status
//     code its HTTP status maps to, with the error text as the message
//     and, when the error reports an `ErrCode()`, an ErrorInfo detail
//     carrying that code as its reason and the service as its domain.
//     A typed error is an expected outcome and is not logged;
//   - a context cancellation or deadline becomes Canceled or
//     DeadlineExceeded;
//   - anything else goes to the [SetHandleUnknownError] handler.
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
	return unknownError.Load().(UnknownErrorHandler)(ctx, err)
}

// serviceOf is the service part of the call's full method
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

// httpCodes maps every HTTP status the error catalogue can declare onto
// the gRPC code with the same meaning. A status outside the table answers
// Unknown, which keeps the message and says nothing false about the cause.
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
