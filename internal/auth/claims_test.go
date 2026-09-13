package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTValidatorNormalizesDelegatedAndWorkloadPermissions(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 13, 19, 0, 0, 0, time.UTC)
	validator, err := NewJWTValidator(JWTConfig{
		Issuer:            "issuer",
		Audience:          "skillet",
		AllowedAlgorithms: []string{"RS256"},
		RequiredScopes:    []string{"skills.search"},
		AttributeClaims:   map[string]string{"groups": "group_ids", "entitlements": "ent"},
		KeyFunc:           func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	delegated := signJWT(t, key, jwt.MapClaims{
		"iss": "issuer", "aud": "skillet", "sub": "user", "organization_id": "acme",
		"scope": "skills.search skills.materialize skills.search",
		"roles": []any{"catalogue.review", "catalogue.review"},
		"group_ids": []any{"team-b", "team-a", "team-a"},
		"ent": "licensed",
		"exp": now.Add(time.Hour).Unix(),
	})
	identity, err := validator.Authenticate("Bearer " + delegated)
	if err != nil {
		t.Fatalf("delegated Authenticate() error = %v", err)
	}
	for _, permission := range []string{"skills.search", "skills.materialize", "catalogue.review"} {
		if !identity.HasPermission(permission) {
			t.Fatalf("missing normalized permission %q: %#v", permission, identity.Permissions)
		}
	}
	if !identity.HasScope("skills.search") || identity.HasScope("catalogue.review") {
		t.Fatalf("delegated scopes = %#v", identity.Scopes)
	}
	if got := identity.Attribute("groups"); !slices.Equal(got, []string{"team-a", "team-b"}) {
		t.Fatalf("groups = %#v", got)
	}
	if got := identity.Attribute("entitlements"); !slices.Equal(got, []string{"licensed"}) {
		t.Fatalf("entitlements = %#v", got)
	}

	workload := signJWT(t, key, jwt.MapClaims{
		"iss": "issuer", "aud": "skillet", "sub": "service", "organization_id": "acme",
		"roles": []any{"skills.materialize", "skills.search"},
		"exp": now.Add(time.Hour).Unix(),
	})
	identity, err = validator.Authenticate("Bearer " + workload)
	if err != nil {
		t.Fatalf("workload Authenticate() error = %v", err)
	}
	if !identity.HasPermission("skills.search") || !identity.HasPermission("skills.materialize") {
		t.Fatalf("workload permissions = %#v", identity.Permissions)
	}
	if len(identity.Scopes) != 0 || len(identity.Attributes) != 0 {
		t.Fatalf("workload normalized identity = %#v", identity)
	}
}

func TestJWTValidatorUsesConfiguredClaimMappings(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 13, 19, 0, 0, 0, time.UTC)
	validator, err := NewJWTValidator(JWTConfig{
		Issuer:            "issuer",
		Audience:          "skillet",
		OrganizationClaim: "tenant",
		ScopeClaim:        "permissions",
		RoleClaim:         "app_permissions",
		AttributeClaims:   map[string]string{"groups": "memberships"},
		RequiredScopes:    []string{"skills.search"},
		AllowedAlgorithms: []string{"RS256"},
		KeyFunc:           func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signJWT(t, key, jwt.MapClaims{
		"iss": "issuer", "aud": "skillet", "sub": "service", "tenant": "acme",
		"permissions": "skills.search",
		"app_permissions": []any{"skills.materialize"},
		"memberships": []any{"engineering"},
		"scope": "ignored.scope", "roles": []any{"ignored.role"},
		"exp": now.Add(time.Hour).Unix(),
	})
	identity, err := validator.Authenticate("Bearer " + token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.HasPermission("ignored.scope") || identity.HasPermission("ignored.role") {
		t.Fatalf("unmapped provider claims leaked into permissions: %#v", identity.Permissions)
	}
	if !identity.HasPermission("skills.search") || !identity.HasPermission("skills.materialize") {
		t.Fatalf("permissions = %#v", identity.Permissions)
	}
}

func TestJWTValidatorMalformedConfiguredClaimsFailClosed(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 13, 19, 0, 0, 0, time.UTC)
	validator, err := NewJWTValidator(JWTConfig{
		Issuer: "issuer", Audience: "skillet", AttributeClaims: map[string]string{"groups": "group_ids"},
		AllowedAlgorithms: []string{"RS256"}, KeyFunc: func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	base := jwt.MapClaims{"iss": "issuer", "aud": "skillet", "sub": "subject", "organization_id": "acme", "exp": now.Add(time.Hour).Unix()}
	tests := []struct {
		name  string
		claim string
		value any
	}{
		{name: "roles wrong type", claim: "roles", value: 7},
		{name: "roles empty string", claim: "roles", value: ""},
		{name: "roles surrounding whitespace", claim: "roles", value: " admin "},
		{name: "roles empty array item", claim: "roles", value: []any{"admin", ""}},
		{name: "scope wrong array item", claim: "scope", value: []any{"skills.search", 7}},
		{name: "scope array whitespace", claim: "scope", value: []any{"skills.search", " skills.materialize"}},
		{name: "configured group wrong type", claim: "group_ids", value: map[string]any{"id": "team"}},
		{name: "configured group empty item", claim: "group_ids", value: []any{"team", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := cloneTestClaims(base)
			claims[tt.claim] = tt.value
			if _, err := validator.Authenticate("Bearer " + signJWT(t, key, claims)); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("error = %v, want ErrUnauthorized", err)
			}
		})
	}

	identity, err := validator.Authenticate("Bearer " + signJWT(t, key, base))
	if err != nil {
		t.Fatalf("missing optional claims should grant nothing, got %v", err)
	}
	if len(identity.Permissions) != 0 || len(identity.Attributes) != 0 {
		t.Fatalf("missing optional claims granted data: %#v", identity)
	}
}

func TestIdentityDoesNotExposeRawClaims(t *testing.T) {
	if _, ok := reflect.TypeOf(Identity{}).FieldByName("Claims"); ok {
		t.Fatal("Identity must not expose raw verified token claims beyond the auth boundary")
	}
}

func TestJWTValidatorRejectsMalformedClaimMappingConfiguration(t *testing.T) {
	base := JWTConfig{
		Issuer: "issuer", Audience: "skillet", AllowedAlgorithms: []string{"RS256"},
		KeyFunc: func(*jwt.Token) (any, error) { return []byte("key"), nil },
	}
	for name, mutate := range map[string]func(*JWTConfig){
		"scope claim whitespace": func(c *JWTConfig) { c.ScopeClaim = " scope" },
		"role claim whitespace": func(c *JWTConfig) { c.RoleClaim = "roles claim" },
		"empty attribute source": func(c *JWTConfig) { c.AttributeClaims = map[string]string{"groups": ""} },
		"attribute name whitespace": func(c *JWTConfig) { c.AttributeClaims = map[string]string{"group ids": "groups"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if _, err := NewJWTValidator(cfg); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}
}
