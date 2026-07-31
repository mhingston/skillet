package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientEmbedSendsOpenAICompatibleRequest(t *testing.T) {
	t.Setenv("SKILLET_TEST_EMBEDDING_KEY", "test-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		var request embeddingRequestPayload
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "embedding-test" || request.Input != "find a plan" || request.Dimensions != 3 {
			t.Errorf("request = %+v", request)
		}
		writeJSON(t, w, responsePayload{Data: []responseData{{Index: 0, Embedding: []float32{1, 2, 3}}}})
	}))
	defer func() { server.CloseClientConnections(); server.Close() }()

	client, err := New(Config{
		BaseURL:    server.URL + "/v1/",
		Model:      "embedding-test",
		Dimensions: 3,
		APIKeyEnv:  "SKILLET_TEST_EMBEDDING_KEY",
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := client.EmbedContext(context.Background(), "find a plan")
	if err != nil {
		t.Fatalf("EmbedContext: %v", err)
	}
	if want := []float32{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("embedding = %v, want %v", got, want)
	}
}

func TestNewRejectsInvalidOrUnboundedConfiguration(t *testing.T) {
	t.Setenv("KEY", "test-key")
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "missing base URL", cfg: Config{Model: "m", Dimensions: 3, APIKeyEnv: "KEY"}, want: "base URL"},
		{name: "relative base URL", cfg: Config{BaseURL: "/v1", Model: "m", Dimensions: 3, APIKeyEnv: "KEY"}, want: "absolute"},
		{name: "base URL credentials", cfg: Config{BaseURL: "https://user:pass@example.test/v1", Model: "m", Dimensions: 3, APIKeyEnv: "KEY"}, want: "credentials"},
		{name: "missing model", cfg: Config{BaseURL: "https://example.test/v1", Dimensions: 3, APIKeyEnv: "KEY"}, want: "model"},
		{name: "invalid dimensions", cfg: Config{BaseURL: "https://example.test/v1", Model: "m", Dimensions: 0, APIKeyEnv: "KEY"}, want: "dimensions"},
		{name: "missing API key env", cfg: Config{BaseURL: "https://example.test/v1", Model: "m", Dimensions: 3}, want: "API key environment"},
		{name: "missing API key value", cfg: Config{BaseURL: "https://example.test/v1", Model: "m", Dimensions: 3, APIKeyEnv: "SKILLET_MISSING_EMBEDDING_KEY"}, want: "API key is not configured"},
		{name: "unbounded input", cfg: Config{BaseURL: "https://example.test/v1", Model: "m", Dimensions: 3, APIKeyEnv: "KEY", MaxInputChars: -1}, want: "input"},
		{name: "unbounded response", cfg: Config{BaseURL: "https://example.test/v1", Model: "m", Dimensions: 3, APIKeyEnv: "KEY", MaxResponseBytes: -1}, want: "response"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "missing API key value" {
				t.Setenv("SKILLET_MISSING_EMBEDDING_KEY", "")
			}
			_, err := New(tt.cfg)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("New error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestClientRejectsInputBeforeMakingRequest(t *testing.T) {
	t.Setenv("SKILLET_TEST_EMBEDDING_KEY", "secret")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := New(Config{
		BaseURL:       server.URL,
		Model:         "m",
		Dimensions:    2,
		APIKeyEnv:     "SKILLET_TEST_EMBEDDING_KEY",
		MaxInputChars: 4,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Embed("too long")
	if err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("Embed error = %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("server calls = %d, want 0", got)
	}
}

func TestClientRetriesTransientResponsesWithExponentialBackoffAndJitter(t *testing.T) {
	t.Setenv("SKILLET_TEST_EMBEDDING_KEY", "secret")
	var calls atomic.Int32
	var waits []time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"secret should not leak"}}`)
			return
		}
		writeJSON(t, w, responsePayload{Data: []responseData{{Index: 0, Embedding: []float32{4, 5}}}})
	}))
	defer server.Close()
	client, err := New(Config{
		BaseURL:        server.URL,
		Model:          "m",
		Dimensions:     2,
		APIKeyEnv:      "SKILLET_TEST_EMBEDDING_KEY",
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
	}, WithSleeper(func(ctx context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}), WithJitter(func() float64 { return 0.5 }))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := client.Embed("query")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !reflect.DeepEqual(got, []float32{4, 5}) {
		t.Fatalf("embedding = %v", got)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
	if want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}; !reflect.DeepEqual(waits, want) {
		t.Fatalf("backoff waits = %v, want %v", waits, want)
	}
}

func TestClientDoesNotRetryPermanentErrorsOrExposeSecrets(t *testing.T) {
	t.Setenv("SKILLET_TEST_EMBEDDING_KEY", "top-secret-key")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"top-secret-key"}`)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Model: "m", Dimensions: 2, APIKeyEnv: "SKILLET_TEST_EMBEDDING_KEY", MaxRetries: 3}, WithSleeper(func(context.Context, time.Duration) error {
		t.Fatal("permanent error was retried")
		return nil
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Embed("query")
	if err == nil || strings.Contains(err.Error(), "top-secret-key") {
		t.Fatalf("error = %v, expected redacted error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestClientValidatesBoundedResponseAndDimensions(t *testing.T) {
	t.Setenv("SKILLET_TEST_EMBEDDING_KEY", "secret")
	tests := []struct {
		name string
		body string
		cfg  Config
		want string
	}{
		{name: "wrong dimensions", body: `{"data":[{"index":0,"embedding":[1]}]}`, want: "dimensions"},
		{name: "missing data", body: `{"data":[]}`, want: "exactly one"},
		{name: "wrong index", body: `{"data":[{"index":1,"embedding":[1,2]}]}`, want: "index"},
		{name: "oversized body", body: `{"data":[{"index":0,"embedding":[1,2]}]}`, cfg: Config{MaxResponseBytes: 8}, want: "response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			cfg := Config{BaseURL: server.URL, Model: "m", Dimensions: 2, APIKeyEnv: "SKILLET_TEST_EMBEDDING_KEY", MaxRetries: 0}
			cfg.MaxResponseBytes = tt.cfg.MaxResponseBytes
			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.Embed("query")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("Embed error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestClientHonorsContextTimeoutWithoutSecretInError(t *testing.T) {
	t.Setenv("SKILLET_TEST_EMBEDDING_KEY", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer func() { server.CloseClientConnections(); server.Close() }()
	client, err := New(Config{BaseURL: server.URL, Model: "m", Dimensions: 2, APIKeyEnv: "SKILLET_TEST_EMBEDDING_KEY", Timeout: 20 * time.Millisecond, MaxRetries: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Embed("query")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timed out") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("timeout error = %v", err)
	}
}

type embeddingRequestPayload struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions"`
}

type responsePayload struct {
	Data []responseData `json:"data"`
}

type responseData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

var _ interface {
	Embed(string) ([]float32, error)
} = (*Client)(nil)
