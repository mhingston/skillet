package config

import (
	"strings"
	"testing"
)

func TestLoadCapabilityScope(t *testing.T) {
	c, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: team-skills\n    url: https://example.com/team-skills.git\n    ref: main\n    poll_interval: 1m\n    capability_scope:\n      namespace: payments\n  - id: repo-skills\n    url: https://example.com/repo-skills.git\n    ref: main\n    poll_interval: 1m\n    capability_scope:\n      namespace: payments\n      repository: checkout-api\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Repositories[0].CapabilityScope; got.Namespace != "payments" || got.Repository != "" {
		t.Fatalf("namespace capability scope = %+v", got)
	}
	if got := c.Repositories[1].CapabilityScope; got.Namespace != "payments" || got.Repository != "checkout-api" {
		t.Fatalf("repository capability scope = %+v", got)
	}
}

func TestLoadRejectsRepositoryCapabilityScopeWithoutNamespace(t *testing.T) {
	_, err := Load(writeConfig(t, "organization:\n  id: demo\nauth:\n  mode: development\nrepositories:\n  - id: repo-skills\n    url: https://example.com/repo-skills.git\n    ref: main\n    poll_interval: 1m\n    capability_scope:\n      repository: checkout-api\n"))
	if err == nil || !strings.Contains(err.Error(), "requires namespace") {
		t.Fatalf("got %v, want capability scope namespace validation error", err)
	}
}
