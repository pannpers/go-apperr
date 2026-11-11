package apperr

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/pannpers/go-apperr/apperr/codes"
)

func TestReason_Is(t *testing.T) {
	errExpired := New(codes.FailedPrecondition, "listing has expired", WithReason("LISTING_EXPIRED"))
	errNotReady := New(codes.FailedPrecondition, "listing is not ready", WithReason("LISTING_NOT_READY"))

	tests := []struct {
		name   string
		err    error
		target error
		want   bool
	}{
		{
			name:   "matches an error with the same reason",
			err:    New(codes.FailedPrecondition, "cannot issue", WithReason("LISTING_EXPIRED")),
			target: errExpired,
			want:   true,
		},
		{
			name:   "does not match an error with another reason of the same code",
			err:    errNotReady,
			target: errExpired,
			want:   false,
		},
		{
			name:   "does not match an error of the same code without a reason",
			err:    New(codes.FailedPrecondition, "cannot issue"),
			target: errExpired,
			want:   false,
		},
		{
			name:   "a target without a reason still matches by code",
			err:    errExpired,
			target: ErrFailedPrecondition,
			want:   true,
		},
		{
			name:   "a target without a reason does not match another code",
			err:    errExpired,
			target: ErrNotFound,
			want:   false,
		},
		{
			name:   "matches after Wrap of a standard error with the reason",
			err:    Wrap(sql.ErrNoRows, codes.FailedPrecondition, "cannot issue", WithReason("LISTING_EXPIRED")),
			target: errExpired,
			want:   true,
		},
		{
			name:   "Wrap keeps the inner reason",
			err:    Wrap(errExpired, codes.FailedPrecondition, "order failed"),
			target: errExpired,
			want:   true,
		},
		{
			name: "Wrap keeps the inner reason when the inner error has its own cause",
			err: Wrap(
				Wrap(sql.ErrNoRows, codes.FailedPrecondition, "cannot issue", WithReason("LISTING_EXPIRED")),
				codes.Internal, "order failed"),
			target: errExpired,
			want:   true,
		},
		{
			name:   "a wrapper's own reason matches",
			err:    Wrap(errNotReady, codes.FailedPrecondition, "order failed", WithReason("LISTING_EXPIRED")),
			target: errExpired,
			want:   true,
		},
		{
			name:   "the inner reason still matches under a wrapper's reason when the inner error has no cause",
			err:    Wrap(errNotReady, codes.FailedPrecondition, "order failed", WithReason("LISTING_EXPIRED")),
			target: errNotReady,
			want:   true,
		},
		{
			name: "the inner reason still matches under a wrapper's reason when the inner error has its own cause",
			err: Wrap(
				Wrap(sql.ErrNoRows, codes.FailedPrecondition, "not ready", WithReason("LISTING_NOT_READY")),
				codes.FailedPrecondition, "order failed", WithReason("LISTING_EXPIRED")),
			target: errNotReady,
			want:   true,
		},
		{
			name:   "an empty reason counts as none",
			err:    New(codes.FailedPrecondition, "cannot issue", WithReason("")),
			target: New(codes.FailedPrecondition, "any", WithReason("")),
			want:   true,
		},
		{
			name:   "a plain reason attribute is not a reason",
			err:    New(codes.FailedPrecondition, "cannot issue", slog.String("reason", "LISTING_EXPIRED")),
			target: errExpired,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("errors.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReason_LogValue(t *testing.T) {
	err := New(codes.FailedPrecondition, "cannot issue", WithReason("LISTING_EXPIRED"))

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Error("failed", slog.Any("error", err))

	var got struct {
		Error struct {
			Attrs map[string]any `json:"attrs"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(buf.Bytes(), &got); jsonErr != nil {
		t.Fatalf("unmarshal log %q: %v", buf.String(), jsonErr)
	}

	if r := got.Error.Attrs["reason"]; r != "LISTING_EXPIRED" {
		t.Errorf("attrs.reason = %v, want LISTING_EXPIRED (log: %s)", r, buf.String())
	}
}

func TestReason_NotInMessage(t *testing.T) {
	err := Wrap(sql.ErrNoRows, codes.PermissionDenied, "scanner refused", WithReason("SCANNER_LINK_TOKEN_UNKNOWN"))

	if strings.Contains(err.Error(), "SCANNER_LINK_TOKEN_UNKNOWN") {
		t.Errorf("Error() = %q, must not contain the reason", err.Error())
	}
}
