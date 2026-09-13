package e2e

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/adapter"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/search"
)

func TestOfflineSkillPlaybookAndToolCoexistInLiveCapabilityIndex(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	scope := mustCapabilityScope(t, "demo", "", "")

	docs := []search.Document{
		{ID: "rev-skill", SkillID: "demo/skill/incident", OrganizationID: "demo", RepositoryID: "skills", Name: "incident-response", Description: "diagnose outage mitigation stakeholder communication runbook skill", TrustLevel: "approved", Searchable: true},
		{ID: "rev-playbook", SkillID: "demo/playbook/release", OrganizationID: "demo", RepositoryID: "playbooks", Name: "release-rollout", Description: "canary rollout rollback production release playbook sequence", TrustLevel: "approved", Searchable: true},
		{ID: "rev-tool", SkillID: "mcp-tool:github:issue_triage", OrganizationID: "demo", RepositoryID: "tools", Name: "issue-triage", Description: "search GitHub issues labels assignee state repository query tool", TrustLevel: "approved", Searchable: true},
	}
	mixed, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mixed.Rebuild(docs); err != nil {
		t.Fatal(err)
	}
	service, err := capability.New(mixed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterDetails([]capability.Detail{
		{Descriptor: capability.Descriptor{
			Identity: capability.Identity{ID: docs[1].SkillID, Kind: capability.KindPlaybook}, Name: docs[1].Name, Description: docs[1].Description,
			Scope: scope, Source: capability.Source{RepositoryID: docs[1].RepositoryID}, Provenance: capability.Provenance{RevisionID: docs[1].ID}, TrustLevel: "approved", Status: capability.StatusActive,
		}},
		{Descriptor: capability.Descriptor{
			Identity: capability.Identity{ID: docs[2].SkillID, Kind: capability.KindTool}, Name: docs[2].Name, Description: docs[2].Description,
			Scope: scope, Source: capability.Source{RepositoryID: docs[2].RepositoryID}, Provenance: capability.Provenance{RevisionID: docs[2].ID}, TrustLevel: "approved", Status: capability.StatusActive,
		}, Tool: &capability.ToolDetail{
			ServerID: "github", Name: "issue_triage", InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
			},
			InputSchemaSummary: "type=object; properties=query", InputSchemaSHA256: "fixture", UntrustedMetadata: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	legacy, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Rebuild(docs[:1]); err != nil {
		t.Fatal(err)
	}
	app := httpserver.NewWithSearch(nil, nil, legacy, "demo", candidate.Signer{Key: []byte("coexistence-key")})
	app.ConfigureCapabilities(service)
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := adapter.Client{Server: server.URL + "/mcp"}

	cases := []struct {
		query string
		kind  capability.Kind
	}{
		{"diagnose outage mitigation stakeholder communication runbook", capability.KindSkill},
		{"canary rollout rollback production release playbook sequence", capability.KindPlaybook},
		{"search GitHub issues labels assignee state repository query", capability.KindTool},
	}
	for _, tc := range cases {
		result, err := client.SearchCapabilities(ctx, tc.query, adapter.CapabilityScope{}, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Candidates) == 0 || result.Candidates[0].Capability.Identity.Kind != tc.kind {
			t.Fatalf("query %q top kind = %+v, want %s", tc.query, result.Candidates, tc.kind)
		}
	}
}
