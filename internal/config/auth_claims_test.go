package config

import (
	"strings"
	"testing"
)

func TestAuthClaimMappingsAreOptionalAndOIDCOnly(t *testing.T) {
	validOIDC := func() Config {
		return Config{
			Organization: Organization{ID: "acme"},
			Auth: Auth{Mode: "oidc", Issuer: "https://issuer.example", Audience: "skillet"},
		}
	}

	withoutMapping := validOIDC()
	if err := withoutMapping.Validate(); err != nil {
		t.Fatalf("default OIDC claim mapping should remain valid: %v", err)
	}

	withMapping := validOIDC()
	withMapping.Auth.ScopeClaim = "permissions"
	withMapping.Auth.RoleClaim = "application_roles"
	withMapping.Auth.AttributeClaims = map[string]string{"groups": "group_ids", "entitlements": "licensed_features"}
	if err := withMapping.Validate(); err != nil {
		t.Fatalf("explicit OIDC claim mapping should be valid: %v", err)
	}

	nonOIDC := Config{Organization: Organization{ID: "acme"}, Auth: Auth{Mode: "development", RoleClaim: "roles"}}
	if err := nonOIDC.Validate(); err == nil || !strings.Contains(err.Error(), "only valid in oidc mode") {
		t.Fatalf("development claim mapping error = %v", err)
	}
}

func TestAuthClaimMappingValidationFailsClosed(t *testing.T) {
	base := func() Config {
		return Config{
			Organization: Organization{ID: "acme"},
			Auth: Auth{Mode: "oidc", Issuer: "https://issuer.example", Audience: "skillet"},
		}
	}

	tests := []struct {
		name   string
		mutate func(*Auth)
	}{
		{name: "scope whitespace", mutate: func(a *Auth) { a.ScopeClaim = " permissions" }},
		{name: "role whitespace", mutate: func(a *Auth) { a.RoleClaim = "application roles" }},
		{name: "empty attribute name", mutate: func(a *Auth) { a.AttributeClaims = map[string]string{"": "groups"} }},
		{name: "attribute name whitespace", mutate: func(a *Auth) { a.AttributeClaims = map[string]string{"group ids": "groups"} }},
		{name: "empty attribute source", mutate: func(a *Auth) { a.AttributeClaims = map[string]string{"groups": ""} }},
		{name: "duplicate scope role source", mutate: func(a *Auth) { a.ScopeClaim, a.RoleClaim = "permissions", "permissions" }},
		{name: "custom scope collides with default role", mutate: func(a *Auth) { a.ScopeClaim = "roles" }},
		{name: "custom role collides with default scope", mutate: func(a *Auth) { a.RoleClaim = "scope" }},
		{name: "custom role collides with scope fallback", mutate: func(a *Auth) { a.RoleClaim = "scp" }},
		{name: "scope collides with organization", mutate: func(a *Auth) { a.ScopeClaim = "organization_id" }},
		{name: "attribute collides with organization", mutate: func(a *Auth) { a.AttributeClaims = map[string]string{"groups": "organization_id"} }},
		{name: "attribute collides with default scope", mutate: func(a *Auth) { a.AttributeClaims = map[string]string{"groups": "scope"} }},
		{name: "duplicate attribute source", mutate: func(a *Auth) { a.AttributeClaims = map[string]string{"groups": "shared", "entitlements": "shared"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg.Auth)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want fail-closed mapping error")
			}
		})
	}
}
