package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/adapter"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/mcptool"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfflineMixedCapabilityDiscoveryProgressiveToolDisclosureAndNoExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)

	syncCapabilityFixture(t, ctx, root, "central", "incident-response", "diagnose outage mitigation stakeholder communication incident runbook skill", catalog, packages)
	syncCapabilityFixture(t, ctx, root, "local-a", "architecture", "repository A architecture module dependency boundary analysis skill", catalog, packages)
	skillDocs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(skillDocs) != 2 {
		t.Fatalf("skill routing documents = %d, want 2", len(skillDocs))
	}

	centralTools := loadMCPToolFixture(t, "central-tools.json", mcptool.Options{
		CatalogueID: "central-tools",
		Scope:       mustCapabilityScope(t, "demo", "", ""),
		TrustLevel:  "approved",
	})
	localTools := loadMCPToolFixture(t, "repo-a-tools.json", mcptool.Options{
		CatalogueID: "repo-a-tools",
		Scope:       mustCapabilityScope(t, "demo", "team", "repo-a"),
		TrustLevel:  "approved",
	})

	mixedDocs := append([]search.Document{}, skillDocs...)
	mixedDocs = append(mixedDocs, centralTools.Documents...)
	mixedDocs = append(mixedDocs, localTools.Documents...)
	capabilityIndex, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := capabilityIndex.Rebuild(mixedDocs); err != nil {
		t.Fatal(err)
	}
	capabilities, err := capability.New(capabilityIndex, []capability.SourcePolicy{
		{RepositoryID: "local-a", Scope: mustCapabilityScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "repo-a-tools", Scope: mustCapabilityScope(t, "demo", "team", "repo-a")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := capabilities.RegisterDetails(append(centralTools.Details, localTools.Details...)); err != nil {
		t.Fatal(err)
	}

	// The legacy search index remains skill-only and excludes repository-local
	// skills because search_skills has no scope input.
	legacyIndex, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var legacyDocs []search.Document
	for _, doc := range skillDocs {
		if doc.RepositoryID == "central" {
			legacyDocs = append(legacyDocs, doc)
		}
	}
	if err := legacyIndex.Rebuild(legacyDocs); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	app := httpserver.NewComplete(nil, nil, legacyIndex, "demo", candidate.Signer{Key: []byte("mixed-capability-key")}, packages, packageurl.Signer{Key: []byte("mixed-package-key")}, catalog, baseURL)
	app.ConfigureCapabilities(capabilities)
	httpSrv := &http.Server{Handler: app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"})}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(listener) }()
	t.Cleanup(func() {
		_ = httpSrv.Shutdown(context.Background())
		select {
		case err := <-serveErr:
			if err != nil && err != http.ErrServerClosed {
				t.Errorf("mixed capability server: %v", err)
			}
		default:
		}
	})

	client := adapter.Client{Server: baseURL + "/mcp"}
	centralScope := adapter.CapabilityScope{}
	aScope := adapter.CapabilityScope{Namespace: "team", Repository: "repo-a"}
	bScope := adapter.CapabilityScope{Namespace: "team", Repository: "repo-b"}

	toolSearch, err := client.SearchCapabilities(ctx, "search GitHub issues labels assignee state repository query", centralScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(toolSearch.Candidates) == 0 || toolSearch.Candidates[0].Capability.Identity.Kind != capability.KindTool {
		t.Fatalf("tool query did not rank tool first: %+v", toolSearch.Candidates)
	}
	searchJSON, err := json.Marshal(toolSearch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(searchJSON), `"input_schema":`) {
		t.Fatalf("search_capabilities leaked full tool schema: %s", searchJSON)
	}

	toolCandidate := toolSearch.Candidates[0]
	toolDetail, err := client.DescribeCapability(ctx, toolCandidate.CandidateID, centralScope)
	if err != nil {
		t.Fatal(err)
	}
	if toolDetail.Detail.Tool == nil {
		t.Fatalf("describe_capability did not return tool detail: %+v", toolDetail)
	}
	schemaJSON, err := json.Marshal(toolDetail.Detail.Tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if toolDetail.Detail.Tool.InputSchema["type"] != "object" || !strings.Contains(string(schemaJSON), `"properties"`) {
		t.Fatalf("describe_capability did not progressively disclose tool schema: %+v", toolDetail)
	}
	if toolDetail.Detail.Tool.ExecutionSupported || !toolDetail.Detail.Tool.UntrustedMetadata || toolDetail.MaterializeCandidateID != "" || toolDetail.Detail.MaterializeWith != "" {
		t.Fatalf("tool detail exposed execution/materialisation authority: %+v", toolDetail)
	}

	skillSearch, err := client.SearchCapabilities(ctx, "repository A architecture module dependency boundary analysis", aScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(skillSearch.Candidates) == 0 || skillSearch.Candidates[0].Capability.Identity.Kind != capability.KindSkill {
		t.Fatalf("skill query did not rank skill first: %+v", skillSearch.Candidates)
	}
	both, err := client.SearchCapabilities(ctx, "repository A architecture dependency analysis and fetch deployment status environment revision", aScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsCapabilityKind(both, capability.KindSkill) || !containsCapabilityKind(both, capability.KindTool) {
		t.Fatalf("mixed intent did not return skill and tool: %+v", both.Candidates)
	}
	none, err := client.SearchCapabilities(ctx, "zyzzyva quokka xylophone", aScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Candidates) != 0 {
		t.Fatalf("negative query activated capabilities: %+v", none.Candidates)
	}

	localTool, err := client.SearchCapabilities(ctx, "fetch repository A deployment status environment revision", aScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsCapabilitySource(localTool, "repo-a-tools") {
		t.Fatalf("repository A tool missing in repository A scope: %+v", localTool.Candidates)
	}
	centralLocalQuery, err := client.SearchCapabilities(ctx, "fetch repository A deployment status environment revision", centralScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if containsCapabilitySource(centralLocalQuery, "repo-a-tools") {
		t.Fatalf("repository-local tool leaked to central scope: %+v", centralLocalQuery.Candidates)
	}
	bLocalQuery, err := client.SearchCapabilities(ctx, "fetch repository A deployment status environment revision", bScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if containsCapabilitySource(bLocalQuery, "repo-a-tools") {
		t.Fatalf("repository A tool leaked to repository B scope: %+v", bLocalQuery.Candidates)
	}

	// Discovered tool names are not exposed as invokable Skillet MCP tools, and
	// there is no generic execute capability/tool endpoint.
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "mixed-capability-contract", Version: "1"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "issue_triage" || tool.Name == "deployment_status" || strings.Contains(strings.ToLower(tool.Name), "execute_capability") {
			t.Fatalf("metadata-only discovered tool became executable: %q", tool.Name)
		}
	}

	centralSkill, err := client.SearchCapabilities(ctx, "diagnose outage mitigation stakeholder communication incident runbook", centralScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	var materializable string
	for _, candidate := range centralSkill.Candidates {
		if candidate.Capability.Identity.Kind == capability.KindSkill && candidate.Capability.Source.RepositoryID == "central" {
			materializable = candidate.CandidateID
			break
		}
	}
	if materializable == "" {
		t.Fatalf("central skill missing from mixed discovery: %+v", centralSkill.Candidates)
	}
	describedSkill, err := client.DescribeCapability(ctx, materializable, centralScope)
	if err != nil {
		t.Fatal(err)
	}
	if describedSkill.MaterializeCandidateID != materializable || describedSkill.Detail.MaterializeWith != "materialize_skill" {
		t.Fatalf("skill materialization contract changed: %+v", describedSkill)
	}
	materialized, _, err := client.Materialize(ctx, materializable, filepath.Join(root, "materialized-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Lifecycle.RevisionID != describedSkill.Detail.Descriptor.Provenance.RevisionID {
		t.Fatalf("skill materialization provenance mismatch: materialized=%+v described=%+v", materialized, describedSkill)
	}

	legacy, err := client.Search(ctx, "diagnose outage mitigation stakeholder communication incident runbook", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Candidates) == 0 {
		t.Fatal("legacy search_skills no longer finds central skills")
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyJSON), "mcp-tool:") {
		t.Fatalf("legacy search_skills leaked MCP tool capabilities: %s", legacyJSON)
	}
}

func loadMCPToolFixture(t *testing.T, name string, options mcptool.Options) mcptool.Set {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	set, err := mcptool.Parse(raw, options)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func containsCapabilityKind(result adapter.CapabilitySearchResult, kind capability.Kind) bool {
	for _, candidate := range result.Candidates {
		if candidate.Capability.Identity.Kind == kind {
			return true
		}
	}
	return false
}

func containsCapabilitySource(result adapter.CapabilitySearchResult, source string) bool {
	for _, candidate := range result.Candidates {
		if candidate.Capability.Source.RepositoryID == source {
			return true
		}
	}
	return false
}
