package apperr

import "log/slog"

// reasonKey is the attribute key under which WithReason stores the reason.
const reasonKey = "reason"

// reason is the value type of the attribute WithReason returns. Its own type
// tells it apart from a plain slog.String("reason", ...) that a caller adds
// for logging only.
type reason string

// LogValue implements slog.LogValuer so the reason is logged as a plain string.
func (r reason) LogValue() slog.Value {
	return slog.StringValue(string(r))
}

// WithReason returns an attribute that gives an error a machine-readable reason,
// for New and Wrap. Use it when a caller must tell apart two errors of the same
// code, for example a FailedPrecondition it retries from one it gives up on.
//
// The reason follows the reason of google.rpc.ErrorInfo (AIP-193): a constant in
// UPPER_SNAKE_CASE that identifies the cause, unique within the service.
//
// An error with a reason works as an errors.Is target that matches only errors
// with the same reason. Like errors.Is itself, it looks along the whole chain:
// Wrap keeps the wrapped error's reason, and a wrapper that adds its own reason
// matches both.
//
//	var ErrListingExpired = apperr.New(codes.FailedPrecondition, "listing has expired",
//		apperr.WithReason("LISTING_EXPIRED"))
//
//	return apperr.Wrap(err, codes.FailedPrecondition, "cannot issue",
//		apperr.WithReason("LISTING_EXPIRED"))
//
//	if errors.Is(err, ErrListingExpired) { ... }
//
// The reason is logged as the "reason" attribute. It is not added to the error
// message, and the Connect integration does not send it to clients.
func WithReason(r string) slog.Attr {
	return slog.Any(reasonKey, reason(r))
}

// reasonOf returns the reason of an errors.Is target, or "" if there is none.
// An empty reason counts as none. If a target holds several, the last one,
// which a wrapper added, is its reason.
func reasonOf(attrs []slog.Attr) string {
	for i := len(attrs) - 1; i >= 0; i-- {
		if r, ok := reasonAt(attrs[i]); ok {
			return r
		}
	}

	return ""
}

// hasReason reports whether attrs hold the reason r. Wrap keeps the wrapped
// error's attributes, so attrs hold every reason along a flattened chain.
func hasReason(attrs []slog.Attr, r string) bool {
	for _, a := range attrs {
		if got, ok := reasonAt(a); ok && got == r {
			return true
		}
	}

	return false
}

// reasonAt returns the reason a holds if WithReason created it.
func reasonAt(a slog.Attr) (string, bool) {
	r, ok := a.Value.Any().(reason)
	if !ok || r == "" || a.Key != reasonKey {
		return "", false
	}

	return string(r), true
}
