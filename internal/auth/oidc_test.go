package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestNewOIDCValidatorDiscoversAndCachesRSAKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": "http://" + r.Host + "/keys"})
		case "/keys":
			n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1})
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "key-1", "alg": "RS256", "use": "sig", "n": n, "e": e}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{Issuer: server.URL, Audience: "skillet", RequiredScopes: []string{"skills.search"}, AllowedAlgorithms: []string{"RS256"}})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": server.URL, "aud": "skillet", "sub": "user-1", "organization_id": "demo", "scope": "skills.search", "exp": time.Now().Add(time.Minute).Unix()})
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := validator.Authenticate("Bearer " + signed)
	if err != nil || identity.OrganizationID != "demo" || !identity.HasScope("skills.search") {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
}

func TestNewOIDCValidatorRejectsUnknownKeyID(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "openid-configuration") {
			_, _ = w.Write([]byte(`{"jwks_uri":"` + "http://" + r.Host + `/keys"}`))
			return
		}
		n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"known","n":"` + n + `","e":"AQAB"}]}`))
	}))
	defer server.Close()
	validator, err := NewOIDCValidator(context.Background(), OIDCConfig{Issuer: server.URL, Audience: "a", AllowedAlgorithms: []string{"RS256"}})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": server.URL, "aud": "a", "sub": "s", "organization_id": "o", "exp": time.Now().Add(time.Minute).Unix()})
	token.Header["kid"] = "unknown"
	signed, _ := token.SignedString(key)
	if _, err := validator.Authenticate("Bearer " + signed); err == nil {
		t.Fatal("unknown key ID accepted")
	}
}
