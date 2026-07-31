package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type Provider interface {
	Embed(string) ([]float32, error)
}

type Cache interface {
	Load(provider, model string, dimensions int, routingDigest string) ([]float32, bool, error)
	Save(provider, model string, dimensions int, routingDigest string, vector []float32) error
}

// Cached wraps an embedding provider with a durable, content-addressed cache.
// The routing document itself is never persisted, only its SHA-256 digest.
type Cached struct {
	provider Provider
	cache    Cache
	identity string
	model    string
	dim      int
	observer func()
}

func NewCached(provider Provider, cache Cache, identity, model string, dimensions int) (*Cached, error) {
	if provider == nil || cache == nil {
		return nil, fmt.Errorf("embedding provider and cache are required")
	}
	if identity == "" || model == "" || dimensions < 1 {
		return nil, fmt.Errorf("embedding identity, model, and dimensions are required")
	}
	return &Cached{provider: provider, cache: cache, identity: identity, model: model, dim: dimensions}, nil
}

// SetObserver installs a non-blocking callback invoked only when the wrapped
// provider is called after a cache miss. This distinguishes provider requests
// from durable-cache lookups for metrics.
func (c *Cached) SetObserver(observer func()) { c.observer = observer }

func (c *Cached) Embed(text string) ([]float32, error) {
	digestBytes := sha256.Sum256([]byte(text))
	digest := hex.EncodeToString(digestBytes[:])
	if vector, ok, err := c.cache.Load(c.identity, c.model, c.dim, digest); err != nil {
		return nil, err
	} else if ok {
		return vector, nil
	}
	if c.observer != nil {
		c.observer()
	}
	vector, err := c.provider.Embed(text)
	if err != nil {
		return nil, err
	}
	if len(vector) != c.dim {
		return nil, fmt.Errorf("embedding dimensions = %d, want %d", len(vector), c.dim)
	}
	if err := c.cache.Save(c.identity, c.model, c.dim, digest, vector); err != nil {
		return nil, err
	}
	return vector, nil
}
