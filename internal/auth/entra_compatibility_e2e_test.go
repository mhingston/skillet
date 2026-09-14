package auth_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
)

const (
	entraTenantA            = "11111111-1111-1111-1111-111111111111"
	entraTenantB            = "22222222-2222-2222-2222-222222222222"
	entraAudience           = "api://skillet-test"
	entraDelegatedPermission = "Skillet.Read"
	entraWorkloadPermission  = "Skillet.Read.All"
	entraKeyID               = "entra-test-key"
)

type entraFixture struct {
	issuer    string
	validator authn.Validator
	policy    authz.ClaimsPolicy
	key       *rsa.PrivateKey
	otherKey  *rsa.PrivateKey
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestEntraDelegatedAndWorkloadTokensReachTheSamePolicyModel(t *testing.T) {
	fixture := newEntraFixture(t)

	principals := []struct {
		name           string
		claims         jwt.MapClaims
		permission     string
		delegatedScope bool
	}{
		{
			name:           "delegated user scp",
			claims:         fixture.delegatedClaims(entraTenantA, entraDelegatedPermission),
			permission:     entraDelegatedPermission,
			delegatedScope: true,
		},
		{
			name:       "service principal roles",
			claims:     fixture.workloadClaims(entraTenantA, entraWorkloadPermission),
			permission: entraWorkloadPermission,
		},
	}

	for _, principal := range principals {
		t.Run(principal.name, func(t *testing.T) {
			identity := fixture.authenticate(t, principal.claims, fixture.key)
			if identity.OrganizationID != entraTenantA {
				t.Fatalf("OrganizationID = %q, want %q", identity.OrganizationID, entraTenantA)
			}
			if !identity.HasPermission(principal.permission) {
				t.Fatalf("normalized permissions do not contain %q", principal.permission)
			}
			if principal.delegatedScope && !identity.HasScope(principal.permission) {
				t.Fatalf("delegated scope projection does not contain %q", principal.permission)
			}

			assertDecision(t, fixture.policy.Authorize(context.Background(), identity, authz.ActionCapabilitySearch, authz.Resource{
				OrganizationID: entraTenantA,
				Namespace:      "engineering",
				Repository:     "api",
			}), true, authz.ReasonAllowed)

			assertDecision(t, fixture.policy.Authorize(context.Background(), identity, authz.ActionCapabilitySearch, authz.Resource{
				OrganizationID: entraTenantA,
				Namespace:      "finance",
				Repository:     "api",
			}), false, authz.ReasonNotEntitled)

			assertDecision(t, fixture.policy.Authorize(context.Background(), identity, authz.ActionCapabilitySearch, authz.Resource{
				OrganizationID: entraTenantA,
				Namespace:      "engineering",
				Repository:     "web",
			}), false, authz.ReasonNotEntitled)
		})
	}
}

func TestEntraCompatibilityDenialMatrix(t *testing.T) {
	fixture := newEntraFixture(t)

	principals := []struct {
		name               string
		claims             jwt.MapClaims
		setUnmappedPermission func(jwt.MapClaims)
	}{
		{
			name:   "delegated user",
			claims: fixture.delegatedClaims(entraTenantA, entraDelegatedPermission),
			setUnmappedPermission: func(claims jwt.MapClaims) {
				claims["scp"] = "Skillet.Unmapped"
			},
		},
		{
			name:   "service principal",
			claims: fixture.workloadClaims(entraTenantA, entraWorkloadPermission),
			setUnmappedPermission: func(claims jwt.MapClaims) {
				claims["roles"] = []string{"Skillet.Unmapped.All"}
			},
		},
	}

	for _, principal := range principals {
		t.Run(principal.name, func(t *testing.T) {
			t.Run("issuer", func(t *testing.T) {
				claims := cloneClaims(principal.claims)
				claims["iss"] = "https://login.microsoftonline.test/" + entraTenantB + "/v2.0"
				fixture.assertAuthenticationDenied(t, claims, fixture.key)
			})

			t.Run("audience", func(t *testing.T) {
				claims := cloneClaims(principal.claims)
				claims["aud"] = "api://another-resource"
				fixture.assertAuthenticationDenied(t, claims, fixture.key)
			})

			t.Run("signature", func(t *testing.T) {
				fixture.assertAuthenticationDenied(t, cloneClaims(principal.claims), fixture.otherKey)
			})

			t.Run("tenant", func(t *testing.T) {
				claims := cloneClaims(principal.claims)
				claims["tid"] = entraTenantB
				identity := fixture.authenticate(t, claims, fixture.key)
				assertDecision(t, fixture.policy.Authorize(context.Background(), identity, authz.ActionCapabilitySearch, authz.Resource{
					OrganizationID: entraTenantA,
					Namespace:      "engineering",
					Repository:     "api",
				}), false, authz.ReasonOrganizationMismatch)
			})

			t.Run("unmapped role or scope", func(t *testing.T) {
				claims := cloneClaims(principal.claims)
				principal.setUnmappedPermission(claims)
				identity := fixture.authenticate(t, claims, fixture.key)
				assertDecision(t, fixture.policy.Authorize(context.Background(), identity, authz.ActionCapabilitySearch, authz.Resource{
					OrganizationID: entraTenantA,
					Namespace:      "engineering",
					Repository:     "api",
				}), false, authz.ReasonNotEntitled)
			})
		})
	}
}

func newEntraFixture(t *testing.T) entraFixture {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	issuer := "https://login.microsoftonline.test/" + entraTenantA + "/v2.0"
	jwksURI := "https://login.microsoftonline.test/discovery/v2.0/keys"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case issuer + "/.well-known/openid-configuration":
			return jsonResponse(request, map[string]string{"jwks_uri": jwksURI})
		case jwksURI:
			return jsonResponse(request, map[string]any{"keys": []any{map[string]string{
				"kty": "RSA",
				"kid": entraKeyID,
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e":   "AQAB",
			}}})
		default:
			return nil, fmt.Errorf("unexpected external request in Entra fixture: %s", request.URL)
		}
	})}

	validator, err := authn.NewOIDCValidator(context.Background(), authn.OIDCConfig{
		Issuer:            issuer,
		Audience:          entraAudience,
		OrganizationClaim: "tid",
		ScopeClaim:        "scp",
		RoleClaim:         "roles",
		AllowedAlgorithms: []string{"RS256"},
		HTTPClient:        client,
		CacheTTL:          time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	policy, err := authz.NewClaimsPolicy([]authz.Grant{
		{
			Permissions: []string{entraDelegatedPermission},
			Actions:     []authz.Action{authz.ActionCapabilitySearch},
			Resources: []authz.ResourceRule{{
				Namespace:  "engineering",
				Repository: "api",
			}},
		},
		{
			Permissions: []string{entraWorkloadPermission},
			Actions:     []authz.Action{authz.ActionCapabilitySearch},
			Resources: []authz.ResourceRule{{
				Namespace:  "engineering",
				Repository: "api",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return entraFixture{issuer: issuer, validator: validator, policy: policy, key: key, otherKey: otherKey}
}

func (f entraFixture) delegatedClaims(tenant, permission string) jwt.MapClaims {
	claims := f.baseClaims("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", tenant)
	claims["scp"] = permission
	claims["oid"] = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	claims["azp"] = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	claims["idtyp"] = "user"
	return claims
}

func (f entraFixture) workloadClaims(tenant, permission string) jwt.MapClaims {
	claims := f.baseClaims("dddddddd-dddd-dddd-dddd-dddddddddddd", tenant)
	claims["roles"] = []string{permission}
	claims["oid"] = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	claims["azp"] = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	claims["idtyp"] = "app"
	return claims
}

func (f entraFixture) baseClaims(subject, tenant string) jwt.MapClaims {
	now := time.Now().UTC()
	return jwt.MapClaims{
		"iss": f.issuer,
		"aud": entraAudience,
		"sub": subject,
		"tid": tenant,
		"iat": now.Add(-time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(),
		"ver": "2.0",
	}
}

func (f entraFixture) authenticate(t *testing.T, claims jwt.MapClaims, signingKey *rsa.PrivateKey) authn.Identity {
	t.Helper()
	signed := signToken(t, claims, signingKey)
	identity, err := f.validator.Authenticate("Bearer " + signed)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	return identity
}

func (f entraFixture) assertAuthenticationDenied(t *testing.T, claims jwt.MapClaims, signingKey *rsa.PrivateKey) {
	t.Helper()
	signed := signToken(t, claims, signingKey)
	if _, err := f.validator.Authenticate("Bearer " + signed); !errors.Is(err, authn.ErrUnauthorized) {
		t.Fatalf("Authenticate() error = %v, want ErrUnauthorized", err)
	}
}

func signToken(t *testing.T, claims jwt.MapClaims, key *rsa.PrivateKey) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = entraKeyID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func jsonResponse(request *http.Request, value any) (*http.Response, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    request,
	}, nil
}

func cloneClaims(claims jwt.MapClaims) jwt.MapClaims {
	clone := make(jwt.MapClaims, len(claims))
	for name, value := range claims {
		clone[name] = value
	}
	return clone
}

func assertDecision(t *testing.T, decision authz.Decision, allowed bool, reason string) {
	t.Helper()
	if decision.Allowed != allowed || decision.Reason != reason {
		t.Fatalf("decision = %+v, want allowed=%t reason=%q", decision, allowed, reason)
	}
}
