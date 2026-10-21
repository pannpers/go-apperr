package connectv2_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	connectv1 "connectrpc.com/connect"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	apperrconnect "github.com/pannpers/go-apperr/apperr/connect"
	"github.com/pannpers/go-apperr/apperr/connectv2"
)

// testLogger implements the Logger interface of both the v1 and v2 packages.
type testLogger struct {
	*slog.Logger
}

// Error implements the Logger interface.
func (l *testLogger) Error(ctx context.Context, msg string, err error, args ...slog.Attr) {
	attrs := make([]any, len(args))
	for i, attr := range args {
		attrs[i] = attr
	}
	l.ErrorContext(ctx, msg, append([]any{slog.Any("error", err)}, attrs...)...)
}

// newTestLogger returns a logger that writes JSON to the returned buffer.
func newTestLogger(t *testing.T) (*testLogger, *bytes.Buffer) {
	t.Helper()

	var buf bytes.Buffer
	return &testLogger{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}, &buf
}

// newTestClient registers handler for spec on a v2 server with the error handling
// interceptor and returns an in-process client for it.
func newTestClient(t *testing.T, logger connectv2.Logger, spec connect.Spec, handler connect.ServerFunc) *connect.Client {
	t.Helper()

	server := connect.NewServer(connectv2.NewErrorHandlingInterceptor(logger))
	server.Register(connect.Method{Spec: spec, Handler: handler})
	return connect.NewClient(connectinprocess.New(server))
}

// callUnary calls a unary RPC whose handler returns handlerErr and returns the error the client receives.
func callUnary(t *testing.T, logger connectv2.Logger, handlerErr error) error {
	t.Helper()

	spec := connect.Spec{Procedure: "/TestService/Unary", StreamType: connect.StreamTypeUnary}
	client := newTestClient(t, logger, spec, func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			return err
		}
		if handlerErr != nil {
			return handlerErr
		}
		return stream.Send(&emptypb.Empty{})
	})
	return client.CallUnary(t.Context(), spec, &emptypb.Empty{}, &emptypb.Empty{})
}

func TestHandleError_MatchesV1(t *testing.T) {
	t.Parallel()

	// Each case gives the same error in its v1 and v2 form; the client must see the same code and message.
	type args struct {
		v1Err error
		v2Err error
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "AppErr with a client code",
			args: args{
				v1Err: apperr.New(codes.InvalidArgument, "invalid input"),
				v2Err: apperr.New(codes.InvalidArgument, "invalid input"),
			},
		},
		{
			name: "wrapped AppErr with a client code",
			args: args{
				v1Err: fmt.Errorf("get user: %w", apperr.New(codes.NotFound, "user not found")),
				v2Err: fmt.Errorf("get user: %w", apperr.New(codes.NotFound, "user not found")),
			},
		},
		{
			name: "AppErr with a server code",
			args: args{
				v1Err: apperr.New(codes.Internal, "database connection failed"),
				v2Err: apperr.New(codes.Internal, "database connection failed"),
			},
		},
		{
			name: "existing connect.Error with a client code",
			args: args{
				v1Err: connectv1.NewError(connectv1.CodeInvalidArgument, errors.New("field validation failed")),
				v2Err: connect.NewError(connect.CodeInvalidArgument, "field validation failed"),
			},
		},
		{
			name: "existing connect.Error with a server code",
			args: args{
				v1Err: connectv1.NewError(connectv1.CodeUnavailable, errors.New("try again later")),
				v2Err: connect.NewError(connect.CodeUnavailable, "try again later"),
			},
		},
		{
			name: "plain error",
			args: args{
				v1Err: errors.New("unexpected error"),
				v2Err: errors.New("unexpected error"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v1Logger, _ := newTestLogger(t)
			var v1ConnectErr *connectv1.Error
			if !errors.As(apperrconnect.HandleError(t.Context(), tt.args.v1Err, v1Logger), &v1ConnectErr) {
				t.Fatal("v1 HandleError did not return a *connect.Error")
			}

			v2Logger, _ := newTestLogger(t)
			var gotErr *connect.Error
			if !errors.As(callUnary(t, v2Logger, tt.args.v2Err), &gotErr) {
				t.Fatal("v2 client did not receive a *connect.Error")
			}

			if got, want := uint32(gotErr.Code()), uint32(v1ConnectErr.Code()); got != want {
				t.Errorf("code = %v, want %v (v1)", gotErr.Code(), v1ConnectErr.Code())
			}
			if got, want := gotErr.Message(), v1ConnectErr.Message(); got != want {
				t.Errorf("message = %q, want %q (v1)", got, want)
			}
		})
	}
}

func TestHandleError(t *testing.T) {
	t.Parallel()

	remoteErr := connect.NewError(connect.CodeNotFound, "downstream not found").WithRemote()

	tests := []struct {
		name            string
		err             error
		wantErr         *connect.Error
		wantLogContains []string
	}{
		{
			name:    "return nil when no error occurs",
			err:     nil,
			wantErr: nil,
		},
		{
			name:    "not log AppErr with a client code",
			err:     apperr.New(codes.InvalidArgument, "invalid input"),
			wantErr: connect.NewError(connect.CodeInvalidArgument, "invalid input (invalid_argument)"),
		},
		{
			name:            "log AppErr with a server code and hide its message",
			err:             apperr.New(codes.Internal, "database connection failed"),
			wantErr:         connect.NewError(connect.CodeInternal, "internal server error"),
			wantLogContains: []string{"server error occurred", "database connection failed", `"rpc_method":"/TestService/Method"`},
		},
		{
			name:    "not log connect.Error with a client code",
			err:     connect.NewError(connect.CodeUnauthenticated, "missing token"),
			wantErr: connect.NewError(connect.CodeUnauthenticated, "missing token"),
		},
		{
			name:            "log connect.Error with a server code",
			err:             connect.NewError(connect.CodeUnknown, "unknown failure"),
			wantErr:         connect.NewError(connect.CodeUnknown, "unknown failure"),
			wantLogContains: []string{"server error occurred", `"rpc_method":"/TestService/Method"`},
		},
		{
			name:            "log remote connect.Error even with a client code",
			err:             remoteErr,
			wantErr:         remoteErr,
			wantLogContains: []string{"server error occurred", "downstream not found"},
		},
		{
			name:            "log plain error as Unknown and hide its message",
			err:             errors.New("unexpected error"),
			wantErr:         connect.NewError(connect.CodeUnknown, "internal server error"),
			wantLogContains: []string{"unhandled error occurred", "unexpected error", `"rpc_method":"/TestService/Method"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLogger(t)
			err := connectv2.HandleError(t.Context(), tt.err, logger, slog.String("rpc_method", "/TestService/Method"))

			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				var connectErr *connect.Error
				if !errors.As(err, &connectErr) {
					t.Fatalf("expected *connect.Error, got: %T", err)
				}
				if connectErr.Code() != tt.wantErr.Code() {
					t.Errorf("code = %v, want %v", connectErr.Code(), tt.wantErr.Code())
				}
				if connectErr.Message() != tt.wantErr.Message() {
					t.Errorf("message = %q, want %q", connectErr.Message(), tt.wantErr.Message())
				}
			}

			logOutput := buf.String()
			if len(tt.wantLogContains) == 0 && logOutput != "" {
				t.Errorf("expected no log output but got: %s", logOutput)
			}
			for _, want := range tt.wantLogContains {
				if !strings.Contains(logOutput, want) {
					t.Errorf("log output should contain %q, got: %s", want, logOutput)
				}
			}
		})
	}
}

func TestHandleError_KeepsCause(t *testing.T) {
	t.Parallel()

	errSentinel := errors.New("sentinel")

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "AppErr with a client code",
			err:  apperr.Wrap(errSentinel, codes.NotFound, "user not found"),
		},
		{
			name: "AppErr with a server code",
			err:  apperr.Wrap(errSentinel, codes.Internal, "query failed"),
		},
		{
			name: "plain error",
			err:  fmt.Errorf("query failed: %w", errSentinel),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, _ := newTestLogger(t)
			err := connectv2.HandleError(t.Context(), tt.err, logger)

			if !errors.Is(err, errSentinel) {
				t.Errorf("errors.Is(err, errSentinel) = false, want true")
			}
			if !errors.Is(err, tt.err) {
				t.Errorf("errors.Is(err, original) = false, want true")
			}
			var appErr *apperr.AppErr
			if wantAppErr := errors.As(tt.err, new(*apperr.AppErr)); errors.As(err, &appErr) != wantAppErr {
				t.Errorf("errors.As(err, *apperr.AppErr) = %v, want %v", !wantAppErr, wantAppErr)
			}
		})
	}
}

func TestNewErrorHandlingInterceptor_Streaming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		streamType      connect.StreamType
		handlerErr      error
		wantErr         *connect.Error
		wantLogContains []string
	}{
		{
			name:            "log server-streaming RPC with a server error",
			streamType:      connect.StreamTypeServer,
			handlerErr:      apperr.New(codes.Unavailable, "upstream unavailable"),
			wantErr:         connect.NewError(connect.CodeUnavailable, "internal server error"),
			wantLogContains: []string{"server error occurred", "upstream unavailable", `"rpc_method":"/TestService/Stream"`},
		},
		{
			name:       "not log server-streaming RPC with a client error",
			streamType: connect.StreamTypeServer,
			handlerErr: apperr.New(codes.PermissionDenied, "forbidden"),
			wantErr:    connect.NewError(connect.CodePermissionDenied, "forbidden (permission_denied)"),
		},
		{
			name:            "log bidi-streaming RPC with a plain error",
			streamType:      connect.StreamTypeBidi,
			handlerErr:      errors.New("stream broke"),
			wantErr:         connect.NewError(connect.CodeUnknown, "internal server error"),
			wantLogContains: []string{"unhandled error occurred", "stream broke", `"rpc_method":"/TestService/Stream"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, buf := newTestLogger(t)
			spec := connect.Spec{Procedure: "/TestService/Stream", StreamType: tt.streamType}
			client := newTestClient(t, logger, spec, func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
				if err := stream.Receive(&emptypb.Empty{}); err != nil {
					return err
				}
				// Send one message first so the error ends an open stream.
				if err := stream.Send(&emptypb.Empty{}); err != nil {
					return err
				}
				return tt.handlerErr
			})

			stream := openStream(t, client, spec)

			var gotErr error
			for {
				if err := stream.Receive(&emptypb.Empty{}); err != nil {
					gotErr = err
					break
				}
			}

			var connectErr *connect.Error
			if !errors.As(gotErr, &connectErr) {
				t.Fatalf("expected *connect.Error, got: %v", gotErr)
			}
			if connectErr.Code() != tt.wantErr.Code() {
				t.Errorf("code = %v, want %v", connectErr.Code(), tt.wantErr.Code())
			}
			if connectErr.Message() != tt.wantErr.Message() {
				t.Errorf("message = %q, want %q", connectErr.Message(), tt.wantErr.Message())
			}

			logOutput := buf.String()
			if len(tt.wantLogContains) == 0 && logOutput != "" {
				t.Errorf("expected no log output but got: %s", logOutput)
			}
			for _, want := range tt.wantLogContains {
				if !strings.Contains(logOutput, want) {
					t.Errorf("log output should contain %q, got: %s", want, logOutput)
				}
			}
		})
	}
}

// openStream opens a streaming RPC described by spec and sends one request message.
// The stream is closed when the test ends.
func openStream(t *testing.T, client *connect.Client, spec connect.Spec) connect.ClientStream {
	t.Helper()

	if spec.StreamType == connect.StreamTypeServer {
		stream, err := client.CallServerStream(t.Context(), spec, &emptypb.Empty{})
		if err != nil {
			t.Fatalf("CallServerStream: %v", err)
		}
		t.Cleanup(func() { closeStream(t, stream) })
		return stream
	}

	stream, err := client.CallClientStream(t.Context(), spec)
	if err != nil {
		t.Fatalf("CallClientStream: %v", err)
	}
	t.Cleanup(func() { closeStream(t, stream) })
	if err := stream.Send(&emptypb.Empty{}); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Send: %v", err)
	}
	return stream
}

// closeStream closes stream and fails the test if Close reports an error.
func closeStream(t *testing.T, stream connect.ClientStream) {
	t.Helper()

	if err := stream.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestToConnectCode(t *testing.T) {
	t.Parallel()

	for c := codes.Canceled; c <= codes.Unauthenticated; c++ {
		if got, want := connectv2.ToConnectCode(c).String(), c.String(); got != want {
			t.Errorf("ToConnectCode(%v) = %v, want %v", c, got, want)
		}
	}
}
