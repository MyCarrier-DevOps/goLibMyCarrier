package argocdclient

import (
	"context"
	"errors"
	"testing"
)

func TestRouter_ClientFor(t *testing.T) {
	dev := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
	router := NewRouter(map[Instance]*Config{
		InstanceDev: {ServerUrl: dev.URL, AuthToken: "dev-token"},
	})

	t.Run("configured instance", func(t *testing.T) {
		client, err := router.ClientFor("mycarrier-frontend-dev")
		if err != nil {
			t.Fatalf("ClientFor: %v", err)
		}
		if client.baseUrl != dev.URL || client.authToken != "dev-token" {
			t.Errorf("unexpected client %+v", client)
		}
	})

	t.Run("unconfigured instance", func(t *testing.T) {
		_, err := router.ClientFor("mycarrier-frontend-prod")
		if !errors.Is(err, ErrInstanceNotConfigured) {
			t.Fatalf("expected ErrInstanceNotConfigured, got %v", err)
		}
		for _, want := range []string{"mycarrier-frontend-prod", "prod"} {
			if !contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})
}

func TestRouter_RunResourceAction_FailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		configs func(dev, prod string) map[Instance]*Config
	}{
		{"only dev configured", func(dev, _ string) map[Instance]*Config {
			return map[Instance]*Config{InstanceDev: {ServerUrl: dev, AuthToken: "dev-token"}}
		}},
		{"prod with empty token", func(dev, prod string) map[Instance]*Config {
			return map[Instance]*Config{
				InstanceDev:  {ServerUrl: dev, AuthToken: "dev-token"},
				InstanceProd: {ServerUrl: prod, AuthToken: ""},
			}
		}},
		{"prod with empty server url", func(dev, _ string) map[Instance]*Config {
			return map[Instance]*Config{
				InstanceDev:  {ServerUrl: dev, AuthToken: "dev-token"},
				InstanceProd: {ServerUrl: "", AuthToken: "prod-token"},
			}
		}},
		{"prod with nil config", func(dev, _ string) map[Instance]*Config {
			return map[Instance]*Config{
				InstanceDev:  {ServerUrl: dev, AuthToken: "dev-token"},
				InstanceProd: nil,
			}
		}},
	}
	apps := []string{
		"mycarrier-frontend-prod",   // routes PROD
		"production-csp-prod-order", // routes MGMT
	}
	for _, tt := range tests {
		for _, app := range apps {
			t.Run(tt.name+"/"+app, func(t *testing.T) {
				dev := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
				prod := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
				router := NewRouter(tt.configs(dev.URL, prod.URL))

				err := router.RunResourceAction(context.Background(), app, testRolloutRef, ActionAbort)

				if !errors.Is(err, ErrInstanceNotConfigured) {
					t.Fatalf("expected ErrInstanceNotConfigured, got %v", err)
				}
				if n := len(dev.recorded()) + len(prod.recorded()); n != 0 {
					t.Errorf("expected zero HTTP requests, got %d", n)
				}
			})
		}
	}
}

func TestRouter_RunResourceAction_RoutesToOwningInstance(t *testing.T) {
	tests := []struct {
		app      string
		wantDev  bool
		wantAuth string
	}{
		{"mycarrier-frontend-prod", false, "Bearer prod-token"},
		{"mycarrier-frontend-dev", true, "Bearer dev-token"},
	}
	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			dev := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
			prod := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
			router := NewRouter(map[Instance]*Config{
				InstanceDev:  {ServerUrl: dev.URL, AuthToken: "dev-token"},
				InstanceProd: {ServerUrl: prod.URL, AuthToken: "prod-token"},
			})

			if err := router.RunResourceAction(context.Background(), tt.app, testRolloutRef, ActionAbort); err != nil {
				t.Fatalf("RunResourceAction: %v", err)
			}

			hit, other := prod, dev
			if tt.wantDev {
				hit, other = dev, prod
			}
			if n := len(hit.posts()); n != 1 {
				t.Errorf("expected 1 POST on the owning instance, got %d", n)
			}
			if n := len(other.recorded()); n != 0 {
				t.Errorf("expected no requests on the other instance, got %d", n)
			}
			if got := hit.posts()[0].Auth; got != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, tt.wantAuth)
			}
		})
	}
}

func TestRouter_ListRolloutGroup(t *testing.T) {
	dev := newRolloutServer(t, "suspended")
	prod := newRolloutServer(t, "suspended")
	router := NewRouter(map[Instance]*Config{
		InstanceDev:  {ServerUrl: dev.URL, AuthToken: "dev-token"},
		InstanceProd: {ServerUrl: prod.URL, AuthToken: "prod-token"},
	})

	got, err := router.ListRolloutGroup(context.Background(), "mycarrier-frontend-prod", fixtureGroupB)
	if err != nil {
		t.Fatalf("ListRolloutGroup: %v", err)
	}
	if len(got) != 1 || got[0].Ref.Name != "rt-other" {
		t.Errorf("unexpected statuses %+v", got)
	}
	if n := len(dev.recorded()); n != 0 {
		t.Errorf("expected no requests on the dev instance, got %d", n)
	}
	for _, r := range prod.recorded() {
		if r.Auth != "Bearer prod-token" {
			t.Errorf("Authorization = %q, want the prod token", r.Auth)
		}
	}
	if n := len(prod.recorded()); n == 0 {
		t.Error("expected requests on the prod instance")
	}
}

func TestRouter_ListRolloutGroup_FailsClosed(t *testing.T) {
	dev := newRolloutServer(t, "suspended")
	router := NewRouter(map[Instance]*Config{
		InstanceDev: {ServerUrl: dev.URL, AuthToken: "dev-token"},
	})

	_, err := router.ListRolloutGroup(context.Background(), "mycarrier-frontend-prod", fixtureGroupA)

	if !errors.Is(err, ErrInstanceNotConfigured) {
		t.Fatalf("expected ErrInstanceNotConfigured, got %v", err)
	}
	if n := len(dev.recorded()); n != 0 {
		t.Errorf("expected no HTTP calls, got %d", n)
	}
}
