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
	"sort"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrUnauthorized         = errors.New("unauthorized")
	ErrInvalidConfiguration = errors.New("invalid authentication configuration")
)

// Identity contains only normalized claims trusted by a successful validator.
// Permissions combine delegated scopes and workload/application roles into one
// provider-neutral set. Attributes contains only explicitly configured trusted
// claim mappings. Raw verified token claims never cross this boundary.
//
// Scopes is retained as a compatibility projection for callers that still need
// to distinguish delegated OAuth scopes. New authorization code should consume
// Permissions and Attributes instead.
type Identity struct {
	Subject        string
	OrganizationID string
	Permissions    map[string]struct{}
	Attributes     map[string][]string
	Scopes         map[string]struct{}
}

// HasPermission reports whether the identity has the exact normalized permission.
func (i Identity) HasPermission(permission string) bool {
	_, ok := i.Permissions[permission]
	return ok
}

// HasScope reports whether the identity has the exact delegated scope. It is a
// compatibility helper; provider-neutral authorization should use HasPermission.
func (i Identity) HasScope(scope string) bool {
	_, ok := i.Scopes[scope]
	return ok
}

// Attribute returns a defensive copy of one normalized attribute value list.
func (i Identity) Attribute(name string) []string {
	values := i.Attributes[name]
	return append([]string(nil), values...)
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
	scopes := cloneSet(v.scopes)
	return Identity{
		Subject:        "static",
		OrganizationID: v.organization,
		Permissions:    cloneSet(scopes),
		Attributes:     map[string][]string{},
		Scopes:         scopes,
	}, nil
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
	RoleClaim         string
	AttributeClaims   map[string]string
	RequiredScopes    []string
	AllowedAlgorithms []string
	KeyFunc           jwt.Keyfunc
	ClockSkew         time.Duration
	Now               func() time.Time
}

// JWTValidator validates signed JWTs and extracts a provider-neutral trusted
// identity. When ScopeClaim is empty it reads "scope" and falls back to "scp".
// When RoleClaim is empty it reads "roles". AttributeClaims maps normalized
// provider-neutral attribute names to verified token claim names.
type JWTValidator struct {
	issuer            string
	audience          string
	organizationClaim string
	scopeClaim        string
	roleClaim         string
	attributeClaims   map[string]string
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
	roleClaim := config.RoleClaim
	if roleClaim == "" {
		roleClaim = "roles"
	}
	if err := validateClaimName("organization", organizationClaim); err != nil {
		return JWTValidator{}, err
	}
	if err := validateClaimName("scope", scopeClaim); err != nil {
		return JWTValidator{}, err
	}
	if err := validateClaimName("role", roleClaim); err != nil {
		return JWTValidator{}, err
	}
	attributeClaims := make(map[string]string, len(config.AttributeClaims))
	for name, claim := range config.AttributeClaims {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, " \t\r\n") {
			return JWTValidator{}, fmt.Errorf("%w: attribute name %q must be a non-empty token", ErrInvalidConfiguration, name)
		}
		if err := validateClaimName("attribute "+name, claim); err != nil {
			return JWTValidator{}, err
		}
		attributeClaims[name] = claim
	}
	return JWTValidator{
		issuer:            config.Issuer,
		audience:          config.Audience,
		organizationClaim: organizationClaim,
		scopeClaim:        scopeClaim,
		roleClaim:         roleClaim,
		attributeClaims:   attributeClaims,
		requiredScopes:    scopeSet(config.RequiredScopes),
		allowedAlgorithms: algorithms,
		keyFunc:           config.KeyFunc,
		clockSkew:         config.ClockSkew,
		now:               now,
	}, nil
}

// Authenticate verifies the signature and registered claims before returning
// normalized identity data. All malformed, expired, wrongly scoped, or
// untrusted tokens return ErrUnauthorized without echoing token contents.
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
	roles, ok := extractClaimValues(claims, v.roleClaim)
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	permissions := cloneSet(scopes)
	for role := range roles {
		permissions[role] = struct{}{}
	}
	for required := range v.requiredScopes {
		if _, present := permissions[required]; !present {
			return Identity{}, ErrUnauthorized
		}
	}
	attributes := make(map[string][]string, len(v.attributeClaims))
	for name, claim := range v.attributeClaims {
		values, ok := extractClaimValues(claims, claim)
		if !ok {
			return Identity{}, ErrUnauthorized
		}
		if len(values) == 0 {
			continue
		}
		attributes[name] = sortedSetValues(values)
	}
	return Identity{
		Subject:        subject,
		OrganizationID: organization,
		Permissions:    cloneSet(permissions),
		Attributes:     cloneAttributes(attributes),
		Scopes:         cloneSet(scopes),
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
		parts := strings.Fields(typed)
		if len(parts) == 0 {
			return nil, false
		}
		for _, scope := range parts {
			result[scope] = struct{}{}
		}
	case []any:
		for _, item := range typed {
			scope, ok := item.(string)
			if !ok || strings.TrimSpace(scope) == "" || strings.TrimSpace(scope) != scope || strings.ContainsAny(scope, " \t\r\n") {
				return nil, false
			}
			result[scope] = struct{}{}
		}
	case []string:
		for _, scope := range typed {
			if strings.TrimSpace(scope) == "" || strings.TrimSpace(scope) != scope || strings.ContainsAny(scope, " \t\r\n") {
				return nil, false
			}
			result[scope] = struct{}{}
		}
	default:
		return nil, false
	}
	return result, true
}

// extractClaimValues parses roles and configured attributes. Missing claims are
// optional and grant nothing. A present claim must be either one exact non-empty
// string or an array of exact non-empty strings; malformed values fail closed.
func extractClaimValues(claims jwt.MapClaims, claim string) (map[string]struct{}, bool) {
	value, present := claims[claim]
	if !present {
		return map[string]struct{}{}, true
	}
	result := map[string]struct{}{}
	add := func(value string) bool {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return false
		}
		result[value] = struct{}{}
		return true
	}
	switch typed := value.(type) {
	case string:
		if !add(typed) {
			return nil, false
		}
	case []any:
		for _, item := range typed {
			value, ok := item.(string)
			if !ok || !add(value) {
				return nil, false
			}
		}
	case []string:
		for _, value := range typed {
			if !add(value) {
				return nil, false
			}
		}
	default:
		return nil, false
	}
	return result, true
}

func validateClaimName(kind, claim string) error {
	if strings.TrimSpace(claim) == "" || strings.TrimSpace(claim) != claim || strings.ContainsAny(claim, " \t\r\n") {
		return fmt.Errorf("%w: %s claim must be a non-empty token", ErrInvalidConfiguration, kind)
	}
	return nil
}

func scopeSet(scopes []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			result[scope] = struct{}{}
		}
	}
	return result
}

func cloneSet(values map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}

func cloneAttributes(attributes map[string][]string) map[string][]string {
	result := make(map[string][]string, len(attributes))
	for name, values := range attributes {
		result[name] = append([]string(nil), values...)
	}
	return result
}

func sortedSetValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
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
