package teamsbot

import (
	"context"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// botFrameworkScope is the OAuth2 scope for the Bot Connector API (v3.1/v3.2).
const botFrameworkScope = "https://api.botframework.com/.default"

// tokenURL returns the single-tenant Entra ID token endpoint for tenantID.
func tokenURL(tenantID string) string {
	return "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token"
}

// newTokenSource builds a cached client-credentials token source for the Bot
// Connector. The returned source refreshes the token automatically before
// expiry and is safe for concurrent use.
func newTokenSource(ctx context.Context, cfg *Config) oauth2.TokenSource {
	cc := &clientcredentials.Config{
		ClientID:     cfg.AppID,
		ClientSecret: cfg.AppSecret,
		TokenURL:     tokenURL(cfg.TenantID),
		Scopes:       []string{botFrameworkScope},
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	return cc.TokenSource(ctx)
}
