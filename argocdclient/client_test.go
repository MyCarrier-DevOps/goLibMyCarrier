package argocdclient

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// failingBody is a response body whose read always fails.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func (failingBody) Close() error { return nil }

func TestReadResponse_BodyReadFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{"error status keeps the status code", http.StatusServiceUnavailable, "error reading body of 503 response"},
		{"success status", http.StatusOK, "error reading response body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Body: failingBody{}}

			_, err := readResponse(resp)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
			if !strings.Contains(err.Error(), "connection reset") {
				t.Errorf("error %q does not wrap the read failure", err)
			}
		})
	}
}
