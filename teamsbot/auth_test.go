package teamsbot

import (
	"context"
	"testing"
)

func TestTokenURL(t *testing.T) {
	got := tokenURL("my-tenant")
	want := "https://login.microsoftonline.com/my-tenant/oauth2/v2.0/token"
	if got != want {
		t.Fatalf("tokenURL = %q, want %q", got, want)
	}
}

func TestBotFrameworkScope(t *testing.T) {
	if botFrameworkScope != "https://api.botframework.com/.default" {
		t.Fatalf("scope = %q", botFrameworkScope)
	}
}

func TestNewTokenSource(t *testing.T) {
	cfg := &Config{AppID: "id", AppSecret: "secret", TenantID: "tenant", ServiceURL: defaultServiceURL}
	ts := newTokenSource(context.Background(), cfg)
	if ts == nil {
		t.Fatal("newTokenSource returned nil")
	}
	// Token() would make a network call, so we only assert construction here;
	// end-to-end token use is covered in client_test via WithTokenSource.
}
