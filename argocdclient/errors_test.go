package argocdclient

import (
	"errors"
	"fmt"
	"testing"
)

const notFoundInAppBody = `{"code":3,"message":"Deployment apps bar not found as part of application foo"}`

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{"403", &APIError{StatusCode: 403, Body: "forbidden"}, "client error 403: forbidden"},
		{"404", &APIError{StatusCode: 404, Body: "not found"}, "client error 404: not found"},
		{"400", &APIError{StatusCode: 400, Body: "bad"}, "client error 400: bad"},
		{"409", &APIError{StatusCode: 409, Body: "conflict"}, "client error 409: conflict"},
		{"500", &APIError{StatusCode: 500, Body: "boom"}, "server error 500: boom"},
		{"503", &APIError{StatusCode: 503, Body: "unavailable"}, "server error 503: unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIError_Is(t *testing.T) {
	sentinels := []error{ErrPermissionDenied, ErrNotFound, ErrConflict}
	tests := []struct {
		name string
		err  *APIError
		want error // nil means no sentinel matches
	}{
		{"403 is permission denied", &APIError{StatusCode: 403}, ErrPermissionDenied},
		{"404 is not found", &APIError{StatusCode: 404}, ErrNotFound},
		{"400 not found as part of application is not found",
			&APIError{StatusCode: 400, Body: notFoundInAppBody}, ErrNotFound},
		{"409 is conflict", &APIError{StatusCode: 409}, ErrConflict},
		{"400 without phrase matches nothing", &APIError{StatusCode: 400, Body: "invalid argument"}, nil},
		{"500 matches nothing", &APIError{StatusCode: 500, Body: "boom"}, nil},
		{"401 matches nothing", &APIError{StatusCode: 401}, nil},
		{"403 body with phrase stays permission denied",
			&APIError{StatusCode: 403, Body: notFoundInAppBody}, ErrPermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, s := range sentinels {
				if got, want := errors.Is(tt.err, s), s == tt.want; got != want {
					t.Errorf("errors.Is(%v, %v) = %v, want %v", tt.err, s, got, want)
				}
			}
		})
	}
}

func TestAPIError_AsThroughWrap(t *testing.T) {
	wrapped := fmt.Errorf("listing rollouts: %w", &APIError{StatusCode: 403, Body: "denied"})

	var apiErr *APIError
	if !errors.As(wrapped, &apiErr) {
		t.Fatal("errors.As did not find *APIError")
	}
	if apiErr.StatusCode != 403 || apiErr.Body != "denied" {
		t.Errorf("got %+v", apiErr)
	}
	if !errors.Is(wrapped, ErrPermissionDenied) {
		t.Error("errors.Is(wrapped, ErrPermissionDenied) = false, want true")
	}
}
