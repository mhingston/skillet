// Package embedding contains provider-neutral adapters for dense retrieval.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultTimeout          = 15 * time.Second
	defaultMaxRetries       = 3
	defaultInitialBackoff   = 100 * time.Millisecond
	defaultMaxBackoff       = 2 * time.Second
	defaultMaxInputChars    = 32 * 1024
	defaultMaxRequestBytes  = 256 * 1024
	defaultMaxResponseBytes = 4 * 1024 * 1024
	maxDimensions           = 16 * 1024
	maxRetries              = 10
	maxTimeout              = 5 * time.Minute
	maxInputChars           = 1 << 20
	maxRequestBytes         = 8 << 20
	maxResponseBytes        = 16 << 20
	maxModelLength          = 256
)

// Config describes an OpenAI-compatible embeddings endpoint. BaseURL is the
// API base (for example, https://provider.example/v1); the adapter appends
// /embeddings. APIKeyEnv names an environment variable, never the secret
// itself.
type Config struct {
	BaseURL          string
	Model            string
	Dimensions       int
	APIKeyEnv        string
	Timeout          time.Duration
	MaxRetries       int
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	MaxInputChars    int
	MaxRequestBytes  int
	MaxResponseBytes int
}

// SleepFunc is injectable so retry behavior can be tested without waiting.
type SleepFunc func(context.Context, time.Duration) error

// JitterFunc returns a value in [0,1]. Values outside that range are clamped.
// The resulting delay is adjusted by ±20% around the exponential delay.
type JitterFunc func() float64

// Option customizes transport or timing behavior. These options do not change
// the wire contract and are useful for tests and controlled deployments.
type Option func(*Client)

// WithHTTPClient supplies the HTTP client used for provider calls.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithSleeper supplies the retry backoff sleeper.
func WithSleeper(sleep SleepFunc) Option {
	return func(c *Client) {
		if sleep != nil {
			c.sleep = sleep
		}
	}
}

// WithJitter supplies the retry jitter source.
func WithJitter(jitter JitterFunc) Option {
	return func(c *Client) {
		if jitter != nil {
			c.jitter = jitter
		}
	}
}

// Client implements search.Embedder through Embed and also exposes a
// context-aware method for callers that need request cancellation.
type Client struct {
	endpoint         *url.URL
	model            string
	dimensions       int
	apiKey           string
	timeout          time.Duration
	maxRetries       int
	initialBackoff   time.Duration
	maxBackoff       time.Duration
	maxInputChars    int
	maxRequestBytes  int
	maxResponseBytes int
	httpClient       *http.Client
	sleep            SleepFunc
	jitter           JitterFunc
}

// Identity returns the provider family used in durable embedding cache keys.
func (c *Client) Identity() string { return "openai-compatible" }

// New validates the configuration and loads the API key from the configured
// environment variable. The secret is retained only in memory and is never
// included in returned errors.
func New(config Config, options ...Option) (*Client, error) {
	base, err := parseBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		return nil, errors.New("embedding model is required")
	}
	if len(model) > maxModelLength {
		return nil, fmt.Errorf("embedding model exceeds %d characters", maxModelLength)
	}
	if config.Dimensions < 1 || config.Dimensions > maxDimensions {
		return nil, fmt.Errorf("embedding dimensions must be between 1 and %d", maxDimensions)
	}
	if strings.TrimSpace(config.APIKeyEnv) == "" {
		return nil, errors.New("embedding API key environment variable is required")
	}
	if !validEnvName(config.APIKeyEnv) {
		return nil, errors.New("embedding API key environment variable name is invalid")
	}
	apiKey, ok := os.LookupEnv(config.APIKeyEnv)
	if !ok || strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("embedding API key is not configured")
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 0 || timeout > maxTimeout {
		return nil, fmt.Errorf("embedding timeout must be between 1ns and %s", maxTimeout)
	}
	if config.MaxRetries < 0 || config.MaxRetries > maxRetries {
		return nil, fmt.Errorf("embedding max retries must be between 0 and %d", maxRetries)
	}
	initialBackoff := config.InitialBackoff
	if initialBackoff == 0 {
		initialBackoff = defaultInitialBackoff
	}
	maxBackoff := config.MaxBackoff
	if maxBackoff == 0 {
		maxBackoff = defaultMaxBackoff
	}
	if initialBackoff < 0 || maxBackoff < 0 || maxBackoff < initialBackoff {
		return nil, errors.New("embedding backoff configuration is invalid")
	}
	inputLimit := config.MaxInputChars
	if inputLimit == 0 {
		inputLimit = defaultMaxInputChars
	}
	if inputLimit < 1 || inputLimit > maxInputChars {
		return nil, fmt.Errorf("embedding input limit must be between 1 and %d characters", maxInputChars)
	}
	requestLimit := config.MaxRequestBytes
	if requestLimit == 0 {
		requestLimit = defaultMaxRequestBytes
	}
	if requestLimit < 1 || requestLimit > maxRequestBytes {
		return nil, fmt.Errorf("embedding request limit must be between 1 and %d bytes", maxRequestBytes)
	}
	responseLimit := config.MaxResponseBytes
	if responseLimit == 0 {
		responseLimit = defaultMaxResponseBytes
	}
	if responseLimit < 1 || responseLimit > maxResponseBytes {
		return nil, fmt.Errorf("embedding response limit must be between 1 and %d bytes", maxResponseBytes)
	}

	client := &Client{
		endpoint:         embeddingEndpoint(base),
		model:            model,
		dimensions:       config.Dimensions,
		apiKey:           apiKey,
		timeout:          timeout,
		maxRetries:       config.MaxRetries,
		initialBackoff:   initialBackoff,
		maxBackoff:       maxBackoff,
		maxInputChars:    inputLimit,
		maxRequestBytes:  requestLimit,
		maxResponseBytes: responseLimit,
		httpClient:       &http.Client{},
		sleep:            defaultSleep,
		jitter:           func() float64 { return rand.Float64() },
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}
	return client, nil
}

// Embed satisfies the small search.Embedder interface.
func (c *Client) Embed(input string) ([]float32, error) {
	return c.EmbedContext(context.Background(), input)
}

// EmbedContext sends one input to the provider and validates the response.
// The configured timeout bounds the entire operation, including retries and
// backoff waits.
func (c *Client) EmbedContext(ctx context.Context, input string) ([]float32, error) {
	if ctx == nil {
		return nil, errors.New("embedding context is nil")
	}
	if utf8.RuneCountInString(input) > c.maxInputChars {
		return nil, fmt.Errorf("embedding input exceeds %d characters", c.maxInputChars)
	}
	body, err := json.Marshal(requestPayload{Model: c.model, Input: input, Dimensions: c.dimensions})
	if err != nil {
		return nil, errors.New("failed to encode embedding request")
	}
	if len(body) > c.maxRequestBytes {
		return nil, fmt.Errorf("embedding request exceeds %d bytes", c.maxRequestBytes)
	}

	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		vector, retryable, requestErr := c.request(requestContext, body)
		if requestErr == nil {
			return vector, nil
		}
		if !retryable || attempt >= c.maxRetries {
			return nil, requestErr
		}
		delay := c.backoff(attempt)
		if err := c.sleep(requestContext, delay); err != nil {
			return nil, contextFailure(requestContext, err)
		}
	}
}

type requestPayload struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions"`
}

type responseEnvelope struct {
	Data []responseItem `json:"data"`
}

type responseItem struct {
	Index     int             `json:"index"`
	Embedding json.RawMessage `json:"embedding"`
}

func (c *Client) request(ctx context.Context, body []byte) ([]float32, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, false, errors.New("failed to build embedding request")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, contextFailure(ctx, err)
		}
		return nil, true, errors.New("embedding provider request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.maxResponseBytes)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, contextFailure(ctx, err)
		}
		return nil, true, errors.New("failed to read embedding provider response")
	}
	if len(data) > c.maxResponseBytes {
		return nil, false, fmt.Errorf("embedding provider response exceeds %d bytes", c.maxResponseBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, retryableStatus(resp.StatusCode), fmt.Errorf("embedding provider returned HTTP status %d", resp.StatusCode)
	}
	return c.parseResponse(data)
}

func (c *Client) parseResponse(data []byte) ([]float32, bool, error) {
	var envelope responseEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, false, errors.New("embedding provider returned invalid JSON")
	}
	if len(envelope.Data) != 1 {
		return nil, false, errors.New("embedding provider response must contain exactly one result")
	}
	item := envelope.Data[0]
	if item.Index != 0 {
		return nil, false, errors.New("embedding provider response has an invalid result index")
	}
	var values []json.RawMessage
	if len(item.Embedding) == 0 || json.Unmarshal(item.Embedding, &values) != nil {
		return nil, false, errors.New("embedding provider response has an invalid embedding")
	}
	if len(values) != c.dimensions {
		return nil, false, fmt.Errorf("embedding response dimensions = %d, want %d", len(values), c.dimensions)
	}
	vector := make([]float32, len(values))
	for n, raw := range values {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < -math.MaxFloat32 || value > math.MaxFloat32 {
			return nil, false, errors.New("embedding provider response contains an invalid vector value")
		}
		vector[n] = float32(value)
	}
	return vector, false, nil
}

func (c *Client) backoff(attempt int) time.Duration {
	delay := c.initialBackoff
	for n := 0; n < attempt && delay < c.maxBackoff; n++ {
		if delay > c.maxBackoff/2 {
			delay = c.maxBackoff
			break
		}
		delay *= 2
	}
	if delay > c.maxBackoff {
		delay = c.maxBackoff
	}
	jitter := c.jitter()
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	adjusted := time.Duration(float64(delay) * (0.8 + 0.4*jitter))
	if adjusted < 1 && delay > 0 {
		return 1
	}
	if adjusted > c.maxBackoff {
		return c.maxBackoff
	}
	return adjusted
}

func parseBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("embedding base URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return nil, errors.New("embedding base URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("embedding base URL must use http or https")
	}
	if parsed.User != nil {
		return nil, errors.New("embedding base URL must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("embedding base URL must not contain a query or fragment")
	}
	return parsed, nil
}

func embeddingEndpoint(base *url.URL) *url.URL {
	endpoint := *base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/embeddings"
	endpoint.RawPath = ""
	return &endpoint
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for n, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (n > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func defaultSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func contextFailure(ctx context.Context, _ error) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return errors.New("embedding request timed out")
	case context.Canceled:
		return errors.New("embedding request canceled")
	default:
		return errors.New("embedding provider request failed")
	}
}
