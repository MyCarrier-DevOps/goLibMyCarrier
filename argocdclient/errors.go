package argocdclient

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// notFoundInApplication is the fragment ArgoCD puts in the body of its
// InvalidArgument (HTTP 400) answer when a resource is not part of an Application.
const notFoundInApplication = "not found as part of application"

var (
	// ErrPermissionDenied matches an HTTP 403 answer. With a scoped token ArgoCD
	// answers 403, not 404, for an Application that does not exist, so a 403
	// can mean either a missing permission or a missing Application.
	ErrPermissionDenied = errors.New("argocd: permission denied")

	// ErrNotFound matches an HTTP 404 answer, and the HTTP 400 answer ArgoCD
	// gives when a resource is "not found as part of application".
	ErrNotFound = errors.New("argocd: not found")

	// ErrConflict matches an HTTP 409 answer: the resource's current state does
	// not allow the request.
	ErrConflict = errors.New("argocd: conflict")
)

// APIError is returned for any ArgoCD HTTP answer with a status of 400 or above.
// Body is the raw response body, typically grpc-gateway JSON such as
// {"code":7,"message":"permission denied"}. Match it with errors.Is against
// ErrPermissionDenied, ErrNotFound and ErrConflict, or with errors.As for
// the status code.
type APIError struct {
	StatusCode int
	Body       string
}

// Error renders the error as "client error <code>: <body>" for 4xx statuses
// and "server error <code>: <body>" for 5xx statuses.
func (e *APIError) Error() string {
	kind := "client"
	if e.StatusCode >= http.StatusInternalServerError {
		kind = "server"
	}
	return fmt.Sprintf("%s error %d: %s", kind, e.StatusCode, e.Body)
}

// Is reports whether target is the sentinel this error's status maps to.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrPermissionDenied:
		return e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound ||
			(e.StatusCode == http.StatusBadRequest && strings.Contains(e.Body, notFoundInApplication))
	case ErrConflict:
		return e.StatusCode == http.StatusConflict
	default:
		return false
	}
}
