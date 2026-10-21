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

func TestGetRevisionMetadata_EscapesPathSegments(t *testing.T) {
	srv := newApplicationListServer(t, http.StatusOK, `{"date":"2026-10-20T15:49:29Z"}`)
	if _, err := srv.client().GetRevisionMetadata(context.Background(), "app", "feature/x?y"); err != nil {
		t.Fatalf("GetRevisionMetadata: %v", err)
	}
	if got, want := srv.recorded()[0].Path, "/api/v1/applications/app/revisions/feature%2Fx%3Fy/metadata"; got != want {
		t.Errorf("path = %s, want %s", got, want)
	}
}

func TestGetRevisionMetadata_Errors(t *testing.T) {
	t.Run("empty arguments", func(t *testing.T) {
		c := NewClient(&Config{ServerUrl: "http://unused", AuthToken: testToken})
		if _, err := c.GetRevisionMetadata(context.Background(), "", "abc"); err == nil {
			t.Error("want an error for an empty application name")
		}
		if _, err := c.GetRevisionMetadata(context.Background(), "app", ""); err == nil {
			t.Error("want an error for an empty revision")
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusForbidden, `{"message":"permission denied"}`)
		_, err := srv.client().GetRevisionMetadata(context.Background(), "app", "abc")
		if !errors.Is(err, ErrPermissionDenied) {
			t.Errorf("err = %v, want ErrPermissionDenied", err)
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		srv := newApplicationListServer(t, http.StatusOK, `{`)
		if _, err := srv.client().GetRevisionMetadata(context.Background(), "app", "abc"); err == nil {
			t.Error("want a decode error")
		}
	})
}
