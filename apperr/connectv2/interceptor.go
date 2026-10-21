// Package connectv2 provides AppErr handling for connect-go v2
// (connectrpc.com/connect/v2).
//
// It is the v2 counterpart of [github.com/pannpers/go-apperr/apperr/connect],
// which targets connect-go v1. Both packages return the same code and message
// to clients for the same error, so a service can move handlers from v1 to v2
// one at a time.
package connectv2

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Logger defines the interface for logging operations.
// It has the same method set as the v1 Logger, so one implementation serves both packages.
type Logger interface {
	Error(ctx context.Context, msg string, err error, attrs ...slog.Attr)
}

// NewErrorHandlingInterceptor creates a Connect v2 server interceptor that handles AppErr
// conversion and logging.
//
// A v2 server interceptor wraps every RPC type, so unary and streaming RPCs are both covered.
// For each failed RPC the interceptor calls [HandleError] with the RPC method name as a
// structured log attribute:
//
//	logger.Error(ctx, "server error occurred", err, slog.String("rpc_method", "/service.v1.UserService/GetUser"))
//
// Basic Usage:
//
//	server := connect.NewServer(
//	    connectv2.NewErrorHandlingInterceptor(logger),
//	)
//
// Custom Attributes:
// To log more attributes, write your own interceptor and call [HandleError] directly:
//
//	func NewCustomErrorInterceptor(logger connectv2.Logger) connect.ServerInterceptor {
//	    return func(next connect.ServerFunc) connect.ServerFunc {
//	        return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
//	            err := next(ctx, spec, stream)
//	            if err == nil {
//	                return nil
//	            }
//	            attrs := []slog.Attr{
//	                slog.String("rpc_method", spec.Procedure),
//	                slog.String("stream_type", spec.StreamType.String()),
//	            }
//	            return connectv2.HandleError(ctx, err, logger, attrs...)
//	        }
//	    }
//	}
func NewErrorHandlingInterceptor(logger Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, stream)
			if err != nil {
				rpcAttr := slog.String("rpc_method", spec.Procedure)
				return HandleError(ctx, err, logger, rpcAttr)
			}

			return nil
		}
	}
}

// HandleError converts an error to a Connect v2 error and logs server errors with custom attributes.
//
// The client-visible code and message are the same as those of the v1 HandleError:
//   - nil: returns nil.
//   - AppErr with a client code: returns the AppErr's code and its Error() text as the message.
//   - AppErr with a server code: logs it and returns its code with a generic "internal server error" message.
//   - *connect.Error: returns it unchanged and logs it if its code is a server error.
//     A remote *connect.Error (one received from another RPC) is also logged, because the
//     transport does not forward it and the client receives an Internal error instead.
//   - Any other error: logs it and returns Unknown with a generic "internal server error" message.
//
// In connect-go v2 only the message passed to [connect.NewError] reaches the wire, so the
// message is passed explicitly. The original error is attached with [connect.Error.WithCause],
// which is never serialized, so [errors.Is] and [errors.As] still reach it on the server side.
//
// Server Error Detection:
// Internal, Unknown, DataLoss, Unimplemented, Unavailable and DeadlineExceeded are server
// errors, as defined by [codes.Code.IsServerError].
func HandleError(ctx context.Context, err error, logger Logger, attrs ...slog.Attr) error {
	if err == nil {
		return nil
	}

	var appErr *apperr.AppErr
	if errors.As(err, &appErr) {
		code := ToConnectCode(appErr.Code)
		if appErr.Code.IsServerError() {
			logger.Error(ctx, "server error occurred", appErr, attrs...)
			return connect.NewError(code, internalErrorMessage).WithCause(err)
		}

		return connect.NewError(code, appErr.Error()).WithCause(err)
	}

	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		if connectErr.IsRemote() || codes.Code(connectErr.Code()).IsServerError() {
			logger.Error(ctx, "server error occurred", err, attrs...)
		}

		return connectErr
	}

	// For non-AppErr, non-connect.Error errors, treat as unknown error
	logger.Error(ctx, "unhandled error occurred", err, attrs...)
	return connect.NewError(connect.CodeUnknown, internalErrorMessage).WithCause(err)
}

// ToConnectCode converts a [codes.Code] to a Connect v2 [connect.Code].
// It is the v2 counterpart of [codes.Code.ToConnect]; the numeric values are the same in v1 and v2.
//
// Example:
//
//	appErr := apperr.New(codes.InvalidArgument, "invalid input")
//	connectErr := connect.NewError(connectv2.ToConnectCode(appErr.Code), appErr.Error()).WithCause(appErr)
func ToConnectCode(c codes.Code) connect.Code {
	return connect.Code(c)
}

// internalErrorMessage is the generic message returned to clients for server errors.
// It matches the v1 package so clients see the same text from both.
// The actual error details are logged server-side and kept as the local cause.
const internalErrorMessage = "internal server error"
