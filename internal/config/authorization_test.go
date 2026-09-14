package config

import "testing"

func TestAuthorizationValidationDefaultsToCompatibility(t *testing.T) {
	value := Authorization{}
	if err := value.validate(Auth{Mode: "development"}); err != nil {
		t.Fatal(err)
	}
	if value.Mode != "compatibility" {
		t.Fatalf("mode = %q", value.Mode)
	}
}

func TestAuthorizationClaimsValidation(t *testing.T) {
	valid := Authorization{Mode: "claims", Grants: []AuthorizationGrant{{
		Permissions: []string{"capability.reader"},
		Attributes:  map[string][]string{"groups": {"platform"}},
		Actions:     []string{"capability.search"},
		Resources:   []AuthorizationResourceRule{{Namespace: "engineering", Repository: "api"}},
	}}}
	if err := valid.validate(Auth{Mode: "oidc", AttributeClaims: map[string]string{"groups": "groups"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		auth Auth
		cfg  Authorization
	}{
		{name: "development claims", auth: Auth{Mode: "development"}, cfg: valid},
		{name: "missing grants", auth: Auth{Mode: "oidc"}, cfg: Authorization{Mode: "claims"}},
		{name: "unmapped attribute", auth: Auth{Mode: "oidc"}, cfg: valid},
		{name: "repo without namespace", auth: Auth{Mode: "oidc", AttributeClaims: map[string]string{"groups": "groups"}}, cfg: Authorization{Mode: "claims", Grants: []AuthorizationGrant{{Permissions: []string{"reader"}, Actions: []string{"knowledge.read"}, Resources: []AuthorizationResourceRule{{Repository: "api"}}}}}},
		{name: "grants in compatibility", auth: Auth{Mode: "oidc"}, cfg: Authorization{Mode: "compatibility", Grants: []AuthorizationGrant{{Permissions: []string{"reader"}, Actions: []string{"knowledge.read"}, Resources: []AuthorizationResourceRule{{}}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if err := cfg.validate(tt.auth); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}
