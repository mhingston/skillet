package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "skillet.yaml")
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValidDevelopmentConfigAppliesDefaults(t *testing.T) {
	c, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: demo-skills\n    url: https://github.com/example/skills.git\n    ref: refs/heads/main\n    poll_interval: 1m\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Listen != ":8080" || c.Search.DefaultLimit != 5 || c.Repositories[0].PollInterval.String() != "1m0s" {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestLoadRequiresExplicitAuthenticationMode(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\n"))
	if err == nil || !strings.Contains(err.Error(), "auth.mode") {
		t.Fatalf("got %v, want explicit auth.mode error", err)
	}
}

func TestLoadRejectsDuplicateRepositories(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: same\n    url: https://example.com/a.git\n    ref: main\n    poll_interval: 1m\n  - id: same\n    url: https://example.com/b.git\n    ref: main\n    poll_interval: 1m\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRejectsMaterializationWithoutPublicURL(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nserver:\n  public_base_url: ''\npackages:\n  enabled: true\n"))
	if err == nil || !strings.Contains(err.Error(), "public_base_url") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRejectsTightPolling(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: demo\n    url: https://example.com/a.git\n    ref: main\n    poll_interval: 10s\n"))
	if err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadAppliesAndValidatesPackageURLTTL(t *testing.T) {
	c, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\npackages:\n  signed_url_ttl: 90s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Packages.SignedURLTTL != "90s" {
		t.Fatalf("ttl = %q", c.Packages.SignedURLTTL)
	}
	_, err = Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\npackages:\n  signed_url_ttl: 0s\n"))
	if err == nil || !strings.Contains(err.Error(), "signed_url_ttl") {
		t.Fatalf("got %v, want signed URL TTL validation error", err)
	}
}

func TestLoadRequiresHTTPSOIDCIssuer(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: oidc\n  issuer: http://issuer.example\n  audience: skillet\n"))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("got %v, want HTTPS issuer error", err)
	}
}

func TestLoadRejectsRepositoryURLCredentials(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: repo\n    url: https://user:secret@example.com/skills.git\n    ref: main\n    poll_interval: 1m\n"))
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("got %v, want repository credential rejection", err)
	}
}

func TestLoadAcceptsPlainLocalRepositoryPath(t *testing.T) {
	path := t.TempDir()
	c, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: local-skills\n    path: "+path+"\n    poll_interval: 1m\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Repositories[0].Path != path || c.Repositories[0].Ref != "working-tree" || !strings.HasPrefix(c.Repositories[0].URL, "file://") {
		t.Fatalf("local repository defaults: %+v", c.Repositories[0])
	}
}

func TestLoadRejectsLocalRepositoryWithURL(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: local-skills\n    url: https://example.com/skills.git\n    path: /tmp/skills\n    ref: main\n    poll_interval: 1m\n"))
	if err == nil || !strings.Contains(err.Error(), "either url or path") {
		t.Fatalf("got %v", err)
	}
}
