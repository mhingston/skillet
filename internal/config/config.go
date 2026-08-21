package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultMinimumPollInterval = time.Minute

type Config struct {
	Server       Server       `yaml:"server"`
	Organization Organization `yaml:"organization"`
	Auth         Auth         `yaml:"auth"`
	Packages     Packages     `yaml:"packages"`
	Search       Search       `yaml:"search"`
	Repositories []Repository `yaml:"repositories"`
}

type Server struct {
	Listen              string `yaml:"listen"`
	PublicBaseURL       string `yaml:"public_base_url"`
	DataDir             string `yaml:"data_dir"`
	MCPPath             string `yaml:"mcp_path"`
	ShutdownTimeout     string `yaml:"shutdown_timeout"`
	MaxRequestBodyBytes int64  `yaml:"max_request_body_bytes"`
}

type Organization struct {
	ID          string `yaml:"id"`
	DisplayName string `yaml:"display_name"`
}
type Auth struct {
	Mode              string `yaml:"mode"`
	StaticTokenEnv    string `yaml:"static_token_env"`
	Issuer            string `yaml:"issuer"`
	Audience          string `yaml:"audience"`
	OrganizationClaim string `yaml:"organization_claim"`
}
type Packages struct {
	Enabled              bool   `yaml:"enabled"`
	SignedURLTTL         string `yaml:"signed_url_ttl"`
	SigningKeyEnv        string `yaml:"signing_key_env"`
	MaxFiles             int    `yaml:"max_files"`
	MaxFileBytes         int64  `yaml:"max_file_bytes"`
	MaxUncompressedBytes int64  `yaml:"max_uncompressed_bytes"`
	MaxArchiveBytes      int64  `yaml:"max_archive_bytes"`
}
type Search struct {
	LexicalDepth           int        `yaml:"lexical_depth"`
	VectorDepth            int        `yaml:"vector_depth"`
	RerankDepth            int        `yaml:"rerank_depth"`
	DefaultLimit           int        `yaml:"default_limit"`
	MaxLimit               int        `yaml:"max_limit"`
	RRFK                   int        `yaml:"rrf_k"`
	SearchableMetadataKeys []string   `yaml:"searchable_metadata_keys"`
	Embeddings             Embeddings `yaml:"embeddings"`
	Reranker               Reranker   `yaml:"reranker"`
}
type Embeddings struct {
	Provider   string `yaml:"provider"`
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
	APIKeyEnv  string `yaml:"api_key_env"`
}
type Reranker struct {
	Provider      string `yaml:"provider"`
	BaseURL       string `yaml:"base_url"`
	Model         string `yaml:"model"`
	APIKeyEnv     string `yaml:"api_key_env"`
	Timeout       string `yaml:"timeout"`
	MaxCandidates int    `yaml:"max_candidates"`
}
type MetadataRule struct {
	MetadataKey string `yaml:"metadata_key"`
	Equals      string `yaml:"equals"`
}
type Repository struct {
	ID                string         `yaml:"id"`
	OrganizationID    string         `yaml:"organization_id"`
	URL               string         `yaml:"url"`
	Path              string         `yaml:"path"`
	Ref               string         `yaml:"ref"`
	PollInterval      time.Duration  `yaml:"-"`
	PollIntervalText  string         `yaml:"poll_interval"`
	TrustLevel        string         `yaml:"trust_level"`
	Owner             string         `yaml:"owner"`
	CredentialProfile string         `yaml:"credential_profile"`
	Include           []string       `yaml:"include"`
	Exclude           []string       `yaml:"exclude"`
	SearchExclusions  []MetadataRule `yaml:"search_exclusions"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if strings.Contains(string(b), "${") {
		return Config{}, errors.New("environment interpolation is unsupported; use explicit secret environment fields")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) Validate() error {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = "./data"
	}
	if c.Server.MCPPath == "" {
		c.Server.MCPPath = "/mcp"
	}
	if !strings.HasPrefix(c.Server.MCPPath, "/") {
		return errors.New("server.mcp_path must start with /")
	}
	if c.Organization.ID == "" {
		return errors.New("organization.id is required")
	}
	if c.Auth.Mode == "" {
		return errors.New("auth.mode is required; choose development, static, or oidc explicitly")
	}
	if c.Auth.Mode != "development" && c.Auth.Mode != "static" && c.Auth.Mode != "oidc" {
		return fmt.Errorf("auth.mode %q must be development, static, or oidc", c.Auth.Mode)
	}
	if c.Packages.Enabled || c.Server.PublicBaseURL != "" {
		if c.Server.PublicBaseURL == "" {
			return errors.New("server.public_base_url is required when packages are enabled")
		}
		if u, err := url.Parse(c.Server.PublicBaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			return errors.New("server.public_base_url must be an absolute URL")
		} else if c.Auth.Mode != "development" && u.Scheme != "https" {
			return errors.New("server.public_base_url must use https outside development")
		}
	}
	if c.Packages.SigningKeyEnv == "" {
		c.Packages.SigningKeyEnv = "SKILLET_PACKAGE_SIGNING_KEY"
	}
	if c.Packages.SignedURLTTL == "" {
		c.Packages.SignedURLTTL = "5m"
	} else if ttl, err := time.ParseDuration(c.Packages.SignedURLTTL); err != nil || ttl <= 0 || ttl > 24*time.Hour {
		return errors.New("packages.signed_url_ttl must be a positive duration")
	}
	if c.Packages.MaxFiles < 0 || c.Packages.MaxFileBytes < 0 || c.Packages.MaxUncompressedBytes < 0 || c.Packages.MaxArchiveBytes < 0 {
		return errors.New("package size limits must not be negative")
	}
	if c.Auth.Mode != "development" && c.Packages.Enabled && os.Getenv(c.Packages.SigningKeyEnv) == "" {
		return fmt.Errorf("environment variable %s must be set outside development", c.Packages.SigningKeyEnv)
	}
	if c.Auth.Mode == "static" && c.Auth.StaticTokenEnv == "" {
		return errors.New("auth.static_token_env is required in static mode")
	}
	if c.Auth.Mode == "static" && os.Getenv(c.Auth.StaticTokenEnv) == "" {
		return fmt.Errorf("environment variable %s must be set in static mode", c.Auth.StaticTokenEnv)
	}
	if c.Auth.Mode == "oidc" {
		if c.Auth.Issuer == "" || c.Auth.Audience == "" {
			return errors.New("auth.issuer and auth.audience are required in oidc mode")
		}
		if issuer, err := url.Parse(c.Auth.Issuer); err != nil || issuer.Scheme != "https" || issuer.Host == "" {
			return errors.New("auth.issuer must be an absolute https URL")
		}
	}
	if c.Search.DefaultLimit == 0 {
		c.Search.DefaultLimit = 5
	}
	if c.Search.MaxLimit == 0 {
		c.Search.MaxLimit = 10
	}
	if c.Search.LexicalDepth == 0 {
		c.Search.LexicalDepth = 50
	}
	if c.Search.VectorDepth == 0 {
		c.Search.VectorDepth = 50
	}
	if c.Search.RerankDepth == 0 {
		c.Search.RerankDepth = 20
	}
	if c.Search.RRFK == 0 {
		c.Search.RRFK = 60
	}
	if c.Search.DefaultLimit < 1 || c.Search.MaxLimit < c.Search.DefaultLimit || c.Search.MaxLimit > 10 {
		return errors.New("search limits must satisfy 1 <= default_limit <= max_limit <= 10")
	}
	if c.Search.LexicalDepth < 1 || c.Search.VectorDepth < 1 || c.Search.RerankDepth < 1 || c.Search.RRFK < 1 {
		return errors.New("search depths and rrf_k must be positive")
	}
	if c.Search.Embeddings.BaseURL != "" {
		if c.Search.Embeddings.Provider == "" {
			c.Search.Embeddings.Provider = "openai-compatible"
		}
		if c.Search.Embeddings.Provider != "openai-compatible" || c.Search.Embeddings.Model == "" || c.Search.Embeddings.Dimensions < 1 || c.Search.Embeddings.APIKeyEnv == "" {
			return errors.New("search.embeddings requires openai-compatible provider, model, dimensions, and api_key_env")
		}
	}
	if c.Search.Reranker.BaseURL != "" {
		if c.Search.Reranker.Provider == "" {
			c.Search.Reranker.Provider = "openai-compatible-listwise"
		}
		if c.Search.Reranker.Provider != "openai-compatible-listwise" || c.Search.Reranker.Model == "" {
			return errors.New("search.reranker requires openai-compatible-listwise provider and model")
		}
		if c.Search.Reranker.Timeout != "" {
			if timeout, err := time.ParseDuration(c.Search.Reranker.Timeout); err != nil || timeout <= 0 {
				return errors.New("search.reranker.timeout must be a positive duration")
			}
		}
		if c.Search.Reranker.MaxCandidates < 0 || c.Search.Reranker.MaxCandidates > 100 {
			return errors.New("search.reranker.max_candidates must be between 1 and 100 when configured")
		}
	}
	seen := map[string]bool{}
	for i := range c.Repositories {
		r := &c.Repositories[i]
		if r.ID == "" {
			return fmt.Errorf("repositories[%d].id is required", i)
		}
		if seen[r.ID] {
			return fmt.Errorf("repositories[%d].id duplicates %q", i, r.ID)
		}
		seen[r.ID] = true
		if r.URL != "" && r.Path != "" {
			return fmt.Errorf("repositories[%d] must specify either url or path, not both", i)
		}
		if r.Path != "" {
			absolute, err := filepath.Abs(r.Path)
			if err != nil {
				return fmt.Errorf("repositories[%d].path: %w", i, err)
			}
			info, err := os.Stat(absolute)
			if err != nil {
				return fmt.Errorf("repositories[%d].path: %w", i, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("repositories[%d].path must be a directory", i)
			}
			r.Path = absolute
			r.URL = (&url.URL{Scheme: "file", Path: absolute}).String()
			if r.Ref == "" {
				r.Ref = "working-tree"
			}
		} else {
			if u, err := url.Parse(r.URL); err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("repositories[%d].url must be an absolute URL", i)
			} else if u.User != nil && (u.Scheme == "http" || u.Scheme == "https") {
				return fmt.Errorf("repositories[%d].url must not contain credentials", i)
			}
			if r.Ref == "" {
				return fmt.Errorf("repositories[%d].ref is required", i)
			}
		}
		if r.PollIntervalText == "" {
			r.PollIntervalText = "15m"
		}
		d, err := time.ParseDuration(r.PollIntervalText)
		if err != nil || d < DefaultMinimumPollInterval {
			return fmt.Errorf("repositories[%d].poll_interval must be at least %s", i, DefaultMinimumPollInterval)
		}
		r.PollInterval = d
		if r.TrustLevel == "" {
			r.TrustLevel = "approved"
		}
	}
	return nil
}
