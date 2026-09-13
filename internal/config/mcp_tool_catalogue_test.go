package config

import "testing"

func TestMCPToolCatalogueConfigurationDefaultsTrustAndRequiresScopedRepositoryNamespace(t *testing.T) {
	cfg := Config{
		Organization: Organization{ID: "demo"},
		Auth:         Auth{Mode: "development"},
		MCPToolCatalogues: []MCPToolCatalogue{{
			ID: "github-tools", Path: "./testdata/github-tools.json",
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.MCPToolCatalogues[0].TrustLevel != "approved" {
		t.Fatalf("trust level = %q, want approved", cfg.MCPToolCatalogues[0].TrustLevel)
	}

	bad := Config{
		Organization: Organization{ID: "demo"},
		Auth:         Auth{Mode: "development"},
		MCPToolCatalogues: []MCPToolCatalogue{{
			ID: "repo-tools", Path: "tools.json", CapabilityScope: CapabilityScope{Repository: "repo-a"},
		}},
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("repository-scoped MCP catalogue accepted without namespace")
	}
}

func TestMCPToolCatalogueConfigurationRejectsRoutingSourceIDCollision(t *testing.T) {
	cfg := Config{
		Organization: Organization{ID: "demo"},
		Auth:         Auth{Mode: "development"},
		Repositories: []Repository{{ID: "shared", URL: "https://example.invalid/repo.git", Ref: "main"}},
		MCPToolCatalogues: []MCPToolCatalogue{{ID: "shared", Path: "tools.json"}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("MCP catalogue routing source collision unexpectedly accepted")
	}
}
