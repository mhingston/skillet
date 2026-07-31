package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestStaticBearerValidator(t *testing.T) {
	v, err := NewStaticBearerValidator("secret-token", "acme", []string{"skills.search", "skills.materialize"})
	if err != nil {
		t.Fatalf("NewStaticBearerValidator() error = %v", err)
	}

	identity, err := v.Authenticate("bEaReR secret-token")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.OrganizationID != "acme" || identity.Subject != "static" {
		t.Fatalf("identity = %#v", identity)
	}
	if !identity.HasScope("skills.search") || identity.HasScope("admin") {
		t.Fatalf("scopes = %#v", identity.Scopes)
	}

	for _, header := range []string{"", "Bearer", "Bearer ", "Basic secret-token", "Bearer wrong", "Bearer secret-token extra", " Bearer secret-token"} {
		if _, err := v.Authenticate(header); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Authenticate(%q) error = %v, want ErrUnauthorized", header, err)
		}
	}
}

func TestStaticBearerValidatorRejectsInvalidConfiguration(t *testing.T) {
	for name, values := range map[string][2]string{
		"empty token":        {"", "acme"},
		"empty organization": {"token", ""},
		"whitespace token":   {" token ", "acme"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewStaticBearerValidator(values[0], values[1], nil); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}
}

func TestJWTValidatorAcceptsTrustedOIDCClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	token := signJWT(t, key, jwt.MapClaims{
		"iss":    "https://issuer.example",
		"aud":    "skillet",
		"sub":    "user-123",
		"org_id": "acme",
		"scope":  "skills.search skills.materialize",
		"iat":    now.Add(-time.Minute).Unix(),
		"exp":    now.Add(10 * time.Minute).Unix(),
		"nbf":    now.Add(-time.Minute).Unix(),
	})

	v, err := NewJWTValidator(JWTConfig{
		Issuer:            "https://issuer.example",
		Audience:          "skillet",
		OrganizationClaim: "org_id",
		RequiredScopes:    []string{"skills.search"},
		AllowedAlgorithms: []string{"RS256"},
		KeyFunc:           func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJWTValidator() error = %v", err)
	}
	identity, err := v.Authenticate("Bearer " + token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.Subject != "user-123" || identity.OrganizationID != "acme" {
		t.Fatalf("identity = %#v", identity)
	}
	if !identity.HasScope("skills.materialize") {
		t.Fatalf("scopes = %#v", identity.Scopes)
	}
}

func TestJWTValidatorAcceptsArrayScopesAndDefaultOrganizationClaim(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	token := signJWT(t, key, jwt.MapClaims{
		"iss":             "issuer",
		"aud":             []any{"skillet", "other"},
		"sub":             "subject",
		"organization_id": "org-a",
		"scp":             []any{"skills.search", "skills.materialize"},
		"exp":             now.Add(time.Minute).Unix(),
	})
	v, err := NewJWTValidator(JWTConfig{
		Issuer:            "issuer",
		Audience:          "skillet",
		RequiredScopes:    []string{"skills.materialize"},
		AllowedAlgorithms: []string{"RS256"},
		KeyFunc:           func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate("Bearer " + token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.OrganizationID != "org-a" || !identity.HasScope("skills.search") {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestJWTValidatorRejectsInvalidClaimsAndAlgorithms(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	base := jwt.MapClaims{"iss": "issuer", "aud": "skillet", "sub": "subject", "org": "acme", "scope": "skills.search", "exp": now.Add(time.Minute).Unix()}
	v, err := NewJWTValidator(JWTConfig{
		Issuer:            "issuer",
		Audience:          "skillet",
		OrganizationClaim: "org",
		RequiredScopes:    []string{"skills.search"},
		AllowedAlgorithms: []string{"RS256"},
		KeyFunc:           func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{name: "wrong issuer", mutate: func(c jwt.MapClaims) { c["iss"] = "other" }},
		{name: "wrong audience", mutate: func(c jwt.MapClaims) { c["aud"] = "other" }},
		{name: "missing subject", mutate: func(c jwt.MapClaims) { delete(c, "sub") }},
		{name: "missing organization", mutate: func(c jwt.MapClaims) { delete(c, "org") }},
		{name: "missing scope", mutate: func(c jwt.MapClaims) { c["scope"] = "nope" }},
		{name: "expired", mutate: func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Second).Unix() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := cloneTestClaims(base)
			tt.mutate(claims)
			if _, err := v.Authenticate("Bearer " + signJWT(t, key, claims)); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("error = %v, want ErrUnauthorized", err)
			}
		})
	}

	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, base)
	unsignedString, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Authenticate("Bearer " + unsignedString); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("none algorithm error = %v, want ErrUnauthorized", err)
	}
}

func TestJWTValidatorRejectsMalformedAuthorizationAndConfiguration(t *testing.T) {
	for name, cfg := range map[string]JWTConfig{
		"missing issuer":       {Audience: "a"},
		"missing audience":     {Issuer: "i"},
		"missing key function": {Issuer: "i", Audience: "a"},
		"missing algorithm":    {Issuer: "i", Audience: "a", KeyFunc: func(*jwt.Token) (any, error) { return []byte("k"), nil }},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewJWTValidator(cfg); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewJWTValidator(JWTConfig{Issuer: "i", Audience: "a", AllowedAlgorithms: []string{"RS256"}, KeyFunc: func(*jwt.Token) (any, error) { return &key.PublicKey, nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"", "Basic abc", "Bearer", "Bearer ", "Bearer one two"} {
		if _, err := v.Authenticate(header); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Authenticate(%q) error = %v, want ErrUnauthorized", header, err)
		}
	}
}

func signJWT(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func cloneTestClaims(in jwt.MapClaims) jwt.MapClaims {
	out := make(jwt.MapClaims, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestIdentityClaimsAreDefensivelyCopied(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	v, err := NewJWTValidator(JWTConfig{Issuer: "i", Audience: "a", AllowedAlgorithms: []string{"RS256"}, KeyFunc: func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate("Bearer " + signJWT(t, key, jwt.MapClaims{"iss": "i", "aud": "a", "sub": "s", "organization_id": "o", "exp": now.Add(time.Minute).Unix(), "scope": "skills.search"}))
	if err != nil {
		t.Fatal(err)
	}
	identity.Scopes["admin"] = struct{}{}
	if identity.HasScope("admin") && !strings.Contains(identity.Subject, "s") {
		t.Fatal("unexpected identity mutation")
	}
}
