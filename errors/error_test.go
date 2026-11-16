package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"testing"
)

func TestFromReturnsAppError(t *testing.T) {
	original := BadRequest("invalid input")
	got := From(original)
	if got != original {
		t.Fatal("expected From to return the original AppError")
	}
	if got.Status != http.StatusBadRequest || got.Code != CodeBadRequest {
		t.Fatalf("unexpected error: %+v", got)
	}
}

func TestFromWrappedAppError(t *testing.T) {
	wrapped := fmt.Errorf("repository: %w", NotFound("user not found"))
	got := From(wrapped)
	if got.Code != CodeNotFound || got.Status != http.StatusNotFound {
		t.Fatalf("unexpected error: %+v", got)
	}
	if got.Message != "user not found" {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestFromUnknownErrorHidesDetails(t *testing.T) {
	got := From(stderrors.New("sql: connection refused"))
	if got.Code != CodeInternal || got.Status != http.StatusInternalServerError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if got.Message != "internal server error" {
		t.Fatalf("message = %q", got.Message)
	}
	if !stderrors.Is(got, got.Cause) {
		t.Fatal("expected cause to be preserved")
	}
}

func TestFromSpecialErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		code   int
		status int
	}{
		{"max bytes", &http.MaxBytesError{Limit: 10}, CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{"deadline", context.DeadlineExceeded, CodeTimeout, http.StatusGatewayTimeout},
		{"canceled", context.Canceled, CodeClientClosed, 499},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := From(tc.err)
			if got.Code != tc.code || got.Status != tc.status {
				t.Fatalf("got (%d, %d), want (%d, %d)", got.Code, got.Status, tc.code, tc.status)
			}
		})
	}
}

func TestAppErrorImmutabilityHelpers(t *testing.T) {
	base := BadRequest("original")

	withMessage := base.WithMessage("changed")
	if base.Message != "original" {
		t.Fatal("WithMessage must not mutate the original error")
	}
	if withMessage.Message != "changed" {
		t.Fatalf("message = %q", withMessage.Message)
	}

	cause := stderrors.New("cause")
	withCause := base.WithCause(cause)
	if base.Cause != nil {
		t.Fatal("WithCause must not mutate the original error")
	}
	if !stderrors.Is(withCause, cause) {
		t.Fatal("expected cause to be wrapped")
	}
	if withCause.Error() == base.Error() {
		t.Fatal("expected Error() to include cause")
	}
}

func TestIsMatchesCode(t *testing.T) {
	err := fmt.Errorf("service: %w", Conflict("duplicate"))
	if !Is(err, CodeConflict) {
		t.Fatal("expected code conflict to match")
	}
	if Is(err, CodeNotFound) {
		t.Fatal("did not expect code not found to match")
	}
	if Is(nil, CodeConflict) {
		t.Fatal("nil error must not match")
	}
}

func TestPredefinedHelpers(t *testing.T) {
	checks := []struct {
		name   string
		err    *AppError
		code   int
		status int
	}{
		{"validation", Validation("bad"), CodeValidationFailed, http.StatusBadRequest},
		{"unauthorized", Unauthorized("no"), CodeUnauthorized, http.StatusUnauthorized},
		{"token expired", TokenExpired(), CodeTokenExpired, http.StatusUnauthorized},
		{"forbidden", Forbidden("no"), CodeForbidden, http.StatusForbidden},
		{"permission", PermissionDenied(), CodePermissionDenied, http.StatusForbidden},
		{"not found", NotFound("no"), CodeNotFound, http.StatusNotFound},
		{"conflict", Conflict("no"), CodeConflict, http.StatusConflict},
		{"too many", TooManyRequests("no"), CodeTooManyRequests, http.StatusTooManyRequests},
		{"payload", PayloadTooLarge(), CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{"timeout", Timeout(), CodeTimeout, http.StatusGatewayTimeout},
		{"unavailable", Unavailable("no"), CodeUnavailable, http.StatusServiceUnavailable},
		{"internal", Internal(""), CodeInternal, http.StatusInternalServerError},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.code || tc.err.Status != tc.status {
				t.Fatalf("got (%d, %d), want (%d, %d)", tc.err.Code, tc.err.Status, tc.code, tc.status)
			}
			if tc.err.Message == "" {
				t.Fatal("message must not be empty")
			}
		})
	}
}
