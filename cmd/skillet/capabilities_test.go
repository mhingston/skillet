package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/config"
	"github.com/mhingston/skillet/internal/search"
)

func TestConfiguredCapabilityServiceAndLegacyIsolation(t *testing.T) {
	docs := []search.Document{
		{ID: "central-rev", SkillID: "demo/central/release", OrganizationID: "demo", RepositoryID: "central", Name: "release", Description: "shared release", Searchable: true},
		{ID: "a-rev", SkillID: "demo/local-a/release", OrganizationID: "demo", RepositoryID: "local-a", Name: "release", Description: "repository A release", Searchable: true},
		{ID: "b-rev", SkillID: "demo/local-b/release", OrganizationID: "demo", RepositoryID: "local-b", Name: "release", Description: "repository B release", Searchable: true},
	}
	cfg := config.Config{
		Organization: config.Organization{ID: "demo"},
		Repositories: []config.Repository{
			{ID: "central"},
			{ID: "local-a", CapabilityScope: config.CapabilityScope{Namespace: "team", Repository: "repo-a"}},
			{ID: "local-b", CapabilityScope: config.CapabilityScope{Namespace: "team", Repository: "repo-b"}},
		},
	}

	legacy := legacyRoutingDocuments(docs, cfg.Repositories)
	if len(legacy) != 1 || legacy[0].RepositoryID != "central" {
		t.Fatalf("legacy routing documents = %+v; want central only", legacy)
	}

	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Rebuild(docs); err != nil {
		t.Fatal(err)
	}
	service, err := configuredCapabilityService(index, cfg)
	if err != nil {
		t.Fatal(err)
	}

	aScope, err := capability.NewScope("demo", "team", "repo-a")
	if err != nil {
		t.Fatal(err)
	}
	aDocs, err := service.List(aScope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertRepositories(t, aDocs, "central", "local-a")

	centralScope, err := capability.NewScope("demo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	centralDocs, err := service.List(centralScope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertRepositories(t, centralDocs, "central")
}

func TestConfiguredCapabilityServiceRejectsMalformedScope(t *testing.T) {
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Organization: config.Organization{ID: "demo"},
		Repositories: []config.Repository{{
			ID:              "local-a",
			CapabilityScope: config.CapabilityScope{Namespace: "team", Repository: "../repo-a"},
		}},
	}
	if _, err := configuredCapabilityService(index, cfg); err == nil {
		t.Fatal("configured capability service accepted malformed repository scope")
	}
}

func TestConfiguredMCPToolCapabilitiesAreMixedOnlyIntoCapabilityIndex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tools.json")
	writeToolSnapshot(t, path, "github", "search_issues", "search GitHub issues labels assignee state")
	cfg := config.Config{
		Organization: config.Organization{ID: "demo"},
		MCPToolCatalogues: []config.MCPToolCatalogue{{ID: "github-tools", Path: path, TrustLevel: "approved"}},
	}
	tools, err := loadConfiguredMCPToolCapabilities(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Documents) != 1 || len(tools.Details) != 1 {
		t.Fatalf("tools = %+v", tools)
	}

	skills := []search.Document{{ID: "skill-rev", SkillID: "demo/central/release", OrganizationID: "demo", RepositoryID: "central", Name: "release", Description: "production release checklist", Searchable: true}}
	mixedIndex, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mixedIndex.Rebuild(capabilityRoutingDocuments(skills, tools.Documents)); err != nil {
		t.Fatal(err)
	}
	service, err := configuredCapabilityService(mixedIndex, cfg, tools)
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := capability.NewScope("demo", "", "")
	results, _, err := service.Search("search GitHub issues labels assignee state", 50, 50, 5, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Capability.Identity.Kind != capability.KindTool {
		t.Fatalf("tool result = %+v", results)
	}

	legacyIndex, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyIndex.Rebuild(legacyRoutingDocuments(skills, cfg.Repositories)); err != nil {
		t.Fatal(err)
	}
	if _, ok := legacyIndex.Document(tools.Documents[0].ID); ok {
		t.Fatal("MCP tool leaked into legacy skill index")
	}
}

func TestConfiguredMCPToolCapabilitiesRejectDuplicateStableIdentityAcrossCatalogues(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.json")
	second := filepath.Join(root, "second.json")
	writeToolSnapshot(t, first, "github", "search_issues", "first description")
	writeToolSnapshot(t, second, "github", "search_issues", "second description")
	cfg := config.Config{
		Organization: config.Organization{ID: "demo"},
		MCPToolCatalogues: []config.MCPToolCatalogue{
			{ID: "tools-a", Path: first, TrustLevel: "approved"},
			{ID: "tools-b", Path: second, TrustLevel: "approved"},
		},
	}
	if _, err := loadConfiguredMCPToolCapabilities(cfg); err == nil {
		t.Fatal("duplicate MCP server/tool identity across catalogues unexpectedly accepted")
	}
}

func writeToolSnapshot(t *testing.T, path, server, tool, description string) {
	t.Helper()
	contents := `{"version":1,"server":{"id":"` + server + `"},"tools":[{"name":"` + tool + `","description":"` + description + `","input_schema":{"type":"object"}}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertRepositories(t *testing.T, docs []search.Document, want ...string) {
	t.Helper()
	got := make(map[string]bool, len(docs))
	for _, doc := range docs {
		got[doc.RepositoryID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("repository %q missing from %v", id, got)
		}
	}
}
