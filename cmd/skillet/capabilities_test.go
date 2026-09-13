package main

import (
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
