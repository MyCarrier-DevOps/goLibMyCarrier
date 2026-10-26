package argocdclient

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestGetRevisionMetadata(t *testing.T) {
	srv := newApplicationListServer(t, http.StatusOK, `{"author":"bot <bot@example.com>",`+
		`"date":"2026-10-20T15:49:29Z","tags":["v1"],"message":"Deploy MC.Example 452198b to dev environment"}`)

	got, err := srv.client().GetRevisionMetadata(context.Background(), "development-dev-example", "a06887f")
	if err != nil {
		t.Fatalf("GetRevisionMetadata: %v", err)
	}
	want := RevisionMetadata{
		Author: "bot <bot@example.com>", Date: time.Date(2026, 10, 20, 15, 49, 29, 0, time.UTC),
		Tags: []string{"v1"}, Message: "Deploy MC.Example 452198b to dev environment",
	}
	if got.Author != want.Author || !got.Date.Equal(want.Date) || got.Message != want.Message ||
		len(got.Tags) != 1 || got.Tags[0] != "v1" {
		t.Errorf("metadata = %+v, want %+v", got, want)
	}
	reqs := srv.recorded()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	wantPath := "/api/v1/applications/development-dev-example/revisions/a06887f/metadata"
	if reqs[0].Method != http.MethodGet || reqs[0].Path != wantPath {
		t.Errorf("request = %s %s, want GET %s", reqs[0].Method, reqs[0].Path, wantPath)
	}
	if reqs[0].Auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q", reqs[0].Auth)
	}
}

// ArgoCD resolves only a commit SHA here (util/git/git.go: ^[0-9A-Fa-f]{7,}$); anything else would be a 500.
func TestGetRevisionMetadata_RejectsWhatArgoCDCannotResolve(t *testing.T) {
	srv := newApplicationListServer(t, http.StatusOK, `{"date":"2026-10-20T15:49:29Z"}`)
	for _, rev := range []string{".", "..", "main", "feature/x?y", "a06887", "v1.2.3", "a06887g"} {
		if _, err := srv.client().GetRevisionMetadata(context.Background(), "app", rev); err == nil {
			t.Errorf("want an error for revision %q", rev)
		}
	}
	if n := len(srv.recorded()); n != 0 {
		t.Errorf("expected no request, got %d", n)
	}
	sha256 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789ABCDEF"
	if _, err := srv.client().GetRevisionMetadata(context.Background(), "app", sha256); err != nil {
		t.Errorf("a SHA-256 commit: %v", err)
	}
}

// ArgoCD sends the Unix epoch when it cannot read the commit's author timestamp; a missing date decodes as zero.
func TestGetRevisionMetadata_UnknownDate(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{"author":"bot"}`,
		"epoch":   `{"author":"bot","date":"1970-01-01T00:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newApplicationListServer(t, http.StatusOK, body)
			got, err := srv.client().GetRevisionMetadata(context.Background(), "app", "a06887f")
			if err != nil {
				t.Fatalf("GetRevisionMetadata: %v", err)
			}
			if !got.Date.IsZero() && got.Date.Unix() != 0 {
				t.Errorf("Date = %v, want zero or the epoch", got.Date)
			}
		})
	}
}

func TestGetRevisionMetadata_Errors(t *testing.T) {
	t.Run("empty arguments", func(t *testing.T) {
		c := NewClient(&Config{ServerUrl: "http://unused", AuthToken: testToken})
		if _, err := c.GetRevisionMetadata(context.Background(), "", "a06887f"); err == nil {
			t.Error("want an error for an empty application name")
		}
		if _, err := c.GetRevisionMetadata(context.Background(), "app", ""); err == nil {
			t.Error("want an error for an empty revision")
		}
	})
	// An unknown commit, or a Helm first source, is a 500 from the repo-server. It is not retried: every attempt would
	// make the repo-server fetch the repository again under its lock.
	t.Run("server error is not retried", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusInternalServerError,
			`{"code":2,"message":"unable to resolve 'a06887f' to a commit SHA"}`)
		_, err := srv.client().GetRevisionMetadata(context.Background(), "app", "a06887f")
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("err = %v, want *APIError 500", err)
		}
		if n := len(srv.recorded()); n != 1 {
			t.Errorf("expected 1 request, got %d", n)
		}
	})
	t.Run("repository not found", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusNotFound, `{"message":"repository not found"}`)
		_, err := srv.client().GetRevisionMetadata(context.Background(), "app", "a06887f")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusOK, `{"date":"2026-10-20T15:49:29Z"}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := srv.client().GetRevisionMetadata(ctx, "app", "a06887f")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusForbidden, `{"message":"permission denied"}`)
		_, err := srv.client().GetRevisionMetadata(context.Background(), "app", "a06887f")
		if !errors.Is(err, ErrPermissionDenied) {
			t.Errorf("err = %v, want ErrPermissionDenied", err)
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusOK, `{`)
		if _, err := srv.client().GetRevisionMetadata(context.Background(), "app", "a06887f"); err == nil {
			t.Error("want a decode error")
		}
	})
}
