package embedding

import "testing"

type memoryCache struct {
	values       map[string][]float32
	loads, saves int
}

func (m *memoryCache) Load(provider, model string, _ int, digest string) ([]float32, bool, error) {
	m.loads++
	v, ok := m.values[provider+model+digest]
	return v, ok, nil
}
func (m *memoryCache) Save(provider, model string, _ int, digest string, vector []float32) error {
	m.saves++
	m.values[provider+model+digest] = vector
	return nil
}

type countingProvider struct{ calls int }

func (p *countingProvider) Embed(string) ([]float32, error) { p.calls++; return []float32{1, 2}, nil }

func TestCachedEmbeddingUsesIdentityAndRoutingDigest(t *testing.T) {
	cache := &memoryCache{values: map[string][]float32{}}
	provider := &countingProvider{}
	cached, err := NewCached(provider, cache, "provider-a", "model-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Embed("plan description"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Embed("plan description"); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || cache.saves != 1 || cache.loads != 2 {
		t.Fatalf("provider=%d saves=%d loads=%d", provider.calls, cache.saves, cache.loads)
	}
}

func TestCachedEmbeddingObserverCountsOnlyProviderMisses(t *testing.T) {
	provider := &countingProvider{}
	cache := &memoryCache{values: map[string][]float32{}}
	cached, err := NewCached(provider, cache, "provider-a", "model-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	var observed int
	cached.SetObserver(func() { observed++ })
	if _, err := cached.Embed("same"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Embed("same"); err != nil {
		t.Fatal(err)
	}
	if observed != 1 || provider.calls != 1 {
		t.Fatalf("observer=%d provider_calls=%d, want one each", observed, provider.calls)
	}
}
