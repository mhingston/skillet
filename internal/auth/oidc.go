package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCConfig configures issuer discovery and a cached JSON Web Key Set.
type OIDCConfig struct {
	Issuer            string
	Audience          string
	OrganizationClaim string
	RequiredScopes    []string
	AllowedAlgorithms []string
	HTTPClient        *http.Client
	CacheTTL          time.Duration
}

// NewOIDCValidator discovers the issuer's JWKS endpoint and returns a JWT
// validator. Keys are refreshed on expiry or an unknown key ID; token claims
// never control the discovery URL.
func NewOIDCValidator(ctx context.Context, config OIDCConfig) (Validator, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidConfiguration)
	}
	issuer := strings.TrimRight(strings.TrimSpace(config.Issuer), "/")
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("%w: issuer must be an absolute HTTP or HTTPS URL", ErrInvalidConfiguration)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	discovery, err := fetchOIDCDiscovery(ctx, client, issuer)
	if err != nil {
		return nil, err
	}
	if discovery.JWKSURI == "" {
		return nil, fmt.Errorf("%w: discovery response has no jwks_uri", ErrInvalidConfiguration)
	}
	if parsedJWKS, err := url.Parse(discovery.JWKSURI); err != nil || parsedJWKS.Host == "" || (parsedJWKS.Scheme != "https" && parsedJWKS.Scheme != "http") {
		return nil, fmt.Errorf("%w: jwks_uri must be an absolute HTTP or HTTPS URL", ErrInvalidConfiguration)
	}
	cacheTTL := config.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 10 * time.Minute
	}
	keys := &jwksCache{client: client, endpoint: discovery.JWKSURI, ttl: cacheTTL}
	if err := keys.refresh(ctx); err != nil {
		return nil, err
	}
	validator, err := NewJWTValidator(JWTConfig{
		Issuer: issuer, Audience: config.Audience, OrganizationClaim: config.OrganizationClaim,
		RequiredScopes: config.RequiredScopes, AllowedAlgorithms: config.AllowedAlgorithms,
		KeyFunc: keys.keyFunc,
	})
	if err != nil {
		return nil, err
	}
	return validator, nil
}

type oidcDiscovery struct {
	JWKSURI string `json:"jwks_uri"`
}

func fetchOIDCDiscovery(ctx context.Context, client *http.Client, issuer string) (oidcDiscovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("%w: invalid discovery request", ErrInvalidConfiguration)
	}
	resp, err := client.Do(req)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("oidc discovery failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oidcDiscovery{}, fmt.Errorf("oidc discovery returned HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("oidc discovery response failed")
	}
	var result oidcDiscovery
	if err := json.Unmarshal(body, &result); err != nil {
		return oidcDiscovery{}, fmt.Errorf("oidc discovery response is invalid")
	}
	return result, nil
}

type jwksCache struct {
	mu       sync.RWMutex
	client   *http.Client
	endpoint string
	ttl      time.Duration
	keys     map[string]any
	expires  time.Time
}

func (c *jwksCache) keyFunc(token *jwt.Token) (any, error) {
	kid, ok := token.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, errors.New("token key ID is required")
	}
	c.mu.RLock()
	key, found := c.keys[kid]
	valid := time.Now().Before(c.expires)
	c.mu.RUnlock()
	if found && valid {
		return key, nil
	}
	if err := c.refresh(context.Background()); err != nil {
		return nil, err
	}
	c.mu.RLock()
	key, found = c.keys[kid]
	c.mu.RUnlock()
	if !found {
		return nil, errors.New("token key ID is unknown")
	}
	return key, nil
}

func (c *jwksCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return errors.New("jwks request failed")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return errors.New("jwks request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("jwks returned HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return errors.New("jwks response failed")
	}
	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return errors.New("jwks response is invalid")
	}
	parsed := make(map[string]any, len(document.Keys))
	for _, raw := range document.Keys {
		var key struct{ Kty, Kid, Alg, Use, N, E string }
		if json.Unmarshal(raw, &key) != nil || key.Kty != "RSA" || key.Kid == "" || key.N == "" || key.E == "" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(key.N)
		e, errE := base64.RawURLEncoding.DecodeString(key.E)
		if errN != nil || errE != nil || len(e) == 0 {
			continue
		}
		parsed[key.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(parsed) == 0 {
		return errors.New("jwks contains no supported RSA keys")
	}
	c.mu.Lock()
	c.keys, c.expires = parsed, time.Now().Add(c.ttl)
	c.mu.Unlock()
	return nil
}
