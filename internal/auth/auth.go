// Package auth provides transport-neutral authentication validators for Skillet.
//
// It deliberately does not know about net/http, configuration, OIDC discovery,
// or key storage. An HTTP adapter supplies the Authorization header and an OIDC
// key resolver; the returned Identity is safe to pass to downstream handlers.
package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrUnauthorized         = errors.New("unauthorized")
	ErrInvalidConfiguration = errors.New("invalid authentication configuration")
)

// Identity contains claims trusted by a successful validator. Scopes are
// represented as a set; claims are copied from the verified token and are not
// used by the package for authorization decisions beyond RequiredScopes.
type Identity struct {
	Subject        string
	OrganizationID string
	Scopes         map[string]struct{}
	Claims         map[string]any
}

// HasScope reports whether the identity has the exact requested scope.
func (i Identity) HasScope(scope string) bool {
	_, ok := i.Scopes[scope]
	return ok
}

// Validator authenticates one HTTP Authorization header.
type Validator interface {
	Authenticate(authorization string) (Identity, error)
}

// StaticBearerValidator validates one fixed bearer token. The token is kept
// only in the validator and is compared in constant time.
type StaticBearerValidator struct {
	token        string
	organization string
	scopes       map[string]struct{}
}

// NewStaticBearerValidator constructs a validator for a controlled deployment.
func NewStaticBearerValidator(token, organizationID string, scopes []string) (StaticBearerValidator, error) {
	if strings.TrimSpace(token) == "" || token != strings.TrimSpace(token) {
		return StaticBearerValidator{}, fmt.Errorf("%w: token must be non-empty and must not contain surrounding whitespace", ErrInvalidConfiguration)
	}
	if strings.TrimSpace(organizationID) == "" {
		return StaticBearerValidator{}, fmt.Errorf("%w: organization ID must be non-empty", ErrInvalidConfiguration)
	}
	return StaticBearerValidator{token: token, organization: organizationID, scopes: scopeSet(scopes)}, nil
}

// Authenticate validates an RFC 6750-style Bearer header.
func (v StaticBearerValidator) Authenticate(authorization string) (Identity, error) {
	credential, ok := bearerCredential(authorization)
	if !ok || len(credential) != len(v.token) || subtle.ConstantTimeCompare([]byte(credential), []byte(v.token)) != 1 {
		return Identity{}, ErrUnauthorized
	}
	return Identity{Subject: "static", OrganizationID: v.organization, Scopes: cloneScopes(v.scopes)}, nil
}

// JWTConfig controls validation of a signed OIDC/JWT access token.
//
// KeyFunc must resolve a key from the verified token header (normally by a
// cached OIDC JWKS implementation). It must not trust an organization or key
// URL supplied by the token itself. AllowedAlgorithms is mandatory to prevent
// algorithm confusion and alg:none acceptance.
type JWTConfig struct {
	Issuer            string
	Audience          string
	OrganizationClaim string
	ScopeClaim        string
	RequiredScopes    []string
	AllowedAlgorithms []string
	KeyFunc           jwt.Keyfunc
	ClockSkew         time.Duration
	Now               func() time.Time
}

// JWTValidator validates signed JWTs and extracts the trusted organization and
// scope claims. It supports space-delimited scope and JSON array scope claims;
// when ScopeClaim is empty it reads "scope" and falls back to the common
// "scp" claim.
type JWTValidator struct {
	issuer            string
	audience          string
	organizationClaim string
	scopeClaim        string
	requiredScopes    map[string]struct{}
	allowedAlgorithms []string
	keyFunc           jwt.Keyfunc
	clockSkew         time.Duration
	now               func() time.Time
}

// NewJWTValidator constructs a validator. OIDC discovery and JWKS refresh are
// intentionally outside this package; inject them through JWTConfig.KeyFunc.
func NewJWTValidator(config JWTConfig) (JWTValidator, error) {
	if strings.TrimSpace(config.Issuer) == "" {
		return JWTValidator{}, fmt.Errorf("%w: issuer is required", ErrInvalidConfiguration)
	}
	if strings.TrimSpace(config.Audience) == "" {
		return JWTValidator{}, fmt.Errorf("%w: audience is required", ErrInvalidConfiguration)
	}
	if config.KeyFunc == nil {
		return JWTValidator{}, fmt.Errorf("%w: key function is required", ErrInvalidConfiguration)
	}
	if config.ClockSkew < 0 {
		return JWTValidator{}, fmt.Errorf("%w: clock skew cannot be negative", ErrInvalidConfiguration)
	}
	algorithms := uniqueNonEmpty(config.AllowedAlgorithms)
	if len(algorithms) == 0 {
		return JWTValidator{}, fmt.Errorf("%w: at least one signing algorithm is required", ErrInvalidConfiguration)
	}
	for _, algorithm := range algorithms {
		if strings.EqualFold(algorithm, jwt.SigningMethodNone.Alg()) {
			return JWTValidator{}, fmt.Errorf("%w: unsigned tokens are not allowed", ErrInvalidConfiguration)
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	organizationClaim := config.OrganizationClaim
	if organizationClaim == "" {
		organizationClaim = "organization_id"
	}
	scopeClaim := config.ScopeClaim
	if scopeClaim == "" {
		scopeClaim = "scope"
	}
	return JWTValidator{
		issuer:            config.Issuer,
		audience:          config.Audience,
		organizationClaim: organizationClaim,
		scopeClaim:        scopeClaim,
		requiredScopes:    scopeSet(config.RequiredScopes),
		allowedAlgorithms: algorithms,
		keyFunc:           config.KeyFunc,
		clockSkew:         config.ClockSkew,
		now:               now,
	}, nil
}

// Authenticate verifies the signature and registered claims before returning
// identity data. All malformed, expired, wrongly scoped, or untrusted tokens
// return ErrUnauthorized without echoing token contents.
func (v JWTValidator) Authenticate(authorization string) (Identity, error) {
	credential, ok := bearerCredential(authorization)
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(v.allowedAlgorithms),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(v.clockSkew),
		jwt.WithTimeFunc(v.now),
	)
	token, err := parser.ParseWithClaims(credential, claims, v.keyFunc)
	if err != nil || token == nil || !token.Valid {
		return Identity{}, ErrUnauthorized
	}
	subject, ok := claims["sub"].(string)
	if !ok || strings.TrimSpace(subject) == "" {
		return Identity{}, ErrUnauthorized
	}
	organization, ok := claims[v.organizationClaim].(string)
	if !ok || strings.TrimSpace(organization) == "" {
		return Identity{}, ErrUnauthorized
	}
	scopes, ok := extractScopes(claims, v.scopeClaim)
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	for required := range v.requiredScopes {
		if _, present := scopes[required]; !present {
			return Identity{}, ErrUnauthorized
		}
	}
	return Identity{
		Subject:        subject,
		OrganizationID: organization,
		Scopes:         cloneScopes(scopes),
		Claims:         cloneClaims(claims),
	}, nil
}

func bearerCredential(header string) (string, bool) {
	if strings.TrimSpace(header) != header || !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return "", false
	}
	credential := header[len("Bearer "):]
	if credential == "" || strings.ContainsAny(credential, " \t\r\n") {
		return "", false
	}
	return credential, true
}

func extractScopes(claims jwt.MapClaims, claim string) (map[string]struct{}, bool) {
	value, present := claims[claim]
	if !present && claim == "scope" {
		value, present = claims["scp"]
	}
	if !present {
		return map[string]struct{}{}, true
	}
	result := map[string]struct{}{}
	switch typed := value.(type) {
	case string:
		for _, scope := range strings.Fields(typed) {
			result[scope] = struct{}{}
		}
	case []any:
		for _, item := range typed {
			scope, ok := item.(string)
			if !ok || strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, " \t\r\n") {
				return nil, false
			}
			result[scope] = struct{}{}
		}
	case []string:
		for _, scope := range typed {
			if strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, " \t\r\n") {
				return nil, false
			}
			result[scope] = struct{}{}
		}
	default:
		return nil, false
	}
	return result, true
}

func scopeSet(scopes []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, scope := range scopes {
		if strings.TrimSpace(scope) != "" {
			result[scope] = struct{}{}
		}
	}
	return result
}

func cloneScopes(scopes map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(scopes))
	for scope := range scopes {
		result[scope] = struct{}{}
	}
	return result
}

func cloneClaims(claims jwt.MapClaims) map[string]any {
	result := make(map[string]any, len(claims))
	for key, value := range claims {
		result[key] = value
	}
	return result
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
