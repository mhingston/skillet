package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/adapter"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/evidence"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/ingest"
	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/lockfile"
	"github.com/mhingston/skillet/internal/mcptool"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/restore"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestOfflineM1IntegratedAcceptance proves the vNext M1 contracts against one
// composed Skillet server. Focused package/E2E tests remain the detailed failure
// diagnostics; this test is deliberately release-level and cross-module.
func TestOfflineM1IntegratedAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)

	// One small organisation: central capabilities plus two repository-local
	// sources with overlapping language, governance cases and one malformed skill.
	centralRoot := filepath.Join(root, "central")
	writeGovernanceSkill(t, centralRoot, "release", "production release verification rollout central skill", nil)
	writeGovernanceSkill(t, centralRoot, "legacy-release", "legacy release workflow retained for explicit selection", map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/central/release",
	})
	writeGovernanceSkill(t, centralRoot, "withdrawn-release", "withdrawn unsafe release workflow", map[string]string{
		governance.StateKey:  string(capability.StatusYanked),
		governance.ReasonKey: "fixture withdrawal",
	})
	if err := os.MkdirAll(filepath.Join(centralRoot, "malformed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(centralRoot, "malformed", "SKILL.md"), []byte("# missing frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	centralResult := syncM1Repository(t, ctx, centralRoot, "central", catalog, packages)
	if centralResult.Admitted != 3 || centralResult.Quarantined != 1 {
		t.Fatalf("central admission = %+v, want 3 admitted and 1 quarantined", centralResult)
	}
	syncCapabilityFixture(t, ctx, root, "local-a", "release-a", "repository A deployment migration release verification local skill", catalog, packages)
	syncCapabilityFixture(t, ctx, root, "local-b", "release-b", "repository B deployment migration release verification local skill", catalog, packages)

	skillDocs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	centralTools := loadMCPToolFixture(t, "central-tools.json", mcptool.Options{
		CatalogueID: "central-tools", Scope: mustCapabilityScope(t, "demo", "", ""), TrustLevel: "approved",
	})
	localTools := loadMCPToolFixture(t, "repo-a-tools.json", mcptool.Options{
		CatalogueID: "repo-a-tools", Scope: mustCapabilityScope(t, "demo", "team", "repo-a"), TrustLevel: "approved",
	})

	playbookDoc := search.Document{
		ID: "fixture-playbook-release-v1", SkillID: "demo/playbooks/canary-release", OrganizationID: "demo", RepositoryID: "playbooks",
		Name: "canary-release", Description: "canary rollout rollback production release playbook sequence", TrustLevel: "approved", Searchable: true,
	}
	mixedDocs := append([]search.Document{}, skillDocs...)
	mixedDocs = append(mixedDocs, centralTools.Documents...)
	mixedDocs = append(mixedDocs, localTools.Documents...)
	mixedDocs = append(mixedDocs, playbookDoc)
	capabilityIndex, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := capabilityIndex.Rebuild(mixedDocs); err != nil {
		t.Fatal(err)
	}
	policies := []capability.SourcePolicy{
		{RepositoryID: "central", Scope: mustCapabilityScope(t, "demo", "", ""), Owner: "platform-team"},
		{RepositoryID: "local-a", Scope: mustCapabilityScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "local-b", Scope: mustCapabilityScope(t, "demo", "team", "repo-b")},
		{RepositoryID: "repo-a-tools", Scope: mustCapabilityScope(t, "demo", "team", "repo-a")},
	}
	capabilities, err := capability.New(capabilityIndex, policies)
	if err != nil {
		t.Fatal(err)
	}
	playbookDetail := capability.Detail{Descriptor: capability.Descriptor{
		Identity: capability.Identity{ID: playbookDoc.SkillID, Kind: capability.KindPlaybook}, Name: playbookDoc.Name, Description: playbookDoc.Description,
		Scope: mustCapabilityScope(t, "demo", "", ""), Source: capability.Source{RepositoryID: playbookDoc.RepositoryID},
		Provenance: capability.Provenance{RevisionID: playbookDoc.ID}, TrustLevel: "approved", Status: capability.StatusActive,
	}}
	if err := capabilities.RegisterDetails(append(append(centralTools.Details, localTools.Details...), playbookDetail)); err != nil {
		t.Fatal(err)
	}

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

	bundleRoot := filepath.Join(root, "knowledge")
	copyFixtureTree(t, filepath.Join("..", "knowledge", "testdata", "okf-v02"), bundleRoot)
	if err := os.WriteFile(filepath.Join(bundleRoot, "plain.md"), []byte("# Local operations note\n\nThe amber-lantern-knowledge-only marker belongs only to organisational knowledge.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	knowledgeService, err := knowledge.Open(ctx, filepath.Join(root, "knowledge-data"), knowledge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer knowledgeService.Close()
	if _, err := knowledgeService.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "org-knowledge", Root: bundleRoot, Locator: "git://fixture/org-knowledge", Revision: "knowledge-v1"}}); err != nil {
		t.Fatal(err)
	}

	app := httpserver.NewComplete(nil, nil, legacyIndex, "demo", candidate.Signer{Key: []byte("m1-candidate-key")}, packages, packageurl.Signer{Key: []byte("m1-package-key")}, catalog, "http://example.invalid")
	app.ConfigureCapabilities(capabilities)
	app.ConfigureKnowledge(knowledgeService)
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := adapter.Client{Server: server.URL + "/mcp"}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "m1-acceptance", Version: "1"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	t.Run("Journey_A_capability_discovery_and_materialisation", func(t *testing.T) {
		aScope := adapter.CapabilityScope{Namespace: "team", Repository: "repo-a"}
		found, err := client.SearchCapabilities(ctx, "repository A deployment migration release verification", aScope, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !containsCapabilitySource(found, "local-a") || !containsCapabilitySource(found, "central") || containsCapabilitySource(found, "local-b") {
			t.Fatalf("repo A scope leakage/missing candidates: %+v", found.Candidates)
		}
		local := findCapabilityCandidate(t, found, "local-a")
		if local.Capability.Identity.Kind != capability.KindSkill {
			t.Fatalf("local capability kind = %s", local.Capability.Identity.Kind)
		}
		detail, err := client.DescribeCapability(ctx, local.CandidateID, aScope)
		if err != nil {
			t.Fatal(err)
		}
		if detail.MaterializeCandidateID != local.CandidateID || detail.Detail.Descriptor.Provenance.RevisionID == "" {
			t.Fatalf("progressive skill detail lost materialisation provenance: %+v", detail)
		}
		materialized, _, err := client.Materialize(ctx, local.CandidateID, filepath.Join(root, "materialized-local-a"))
		if err != nil {
			t.Fatal(err)
		}
		if materialized.Lifecycle.RevisionID != detail.Detail.Descriptor.Provenance.RevisionID || materialized.Lifecycle.Commit == "" || materialized.Lifecycle.Tree == "" || materialized.Package.ArchiveSHA256 == "" {
			t.Fatalf("immutable materialisation provenance = %+v", materialized)
		}

		tool, err := client.SearchCapabilities(ctx, "search GitHub issues labels assignee state repository query", adapter.CapabilityScope{}, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(tool.Candidates) == 0 || tool.Candidates[0].Capability.Identity.Kind != capability.KindTool {
			t.Fatalf("tool discovery = %+v", tool.Candidates)
		}
		playbook, err := client.SearchCapabilities(ctx, "canary rollout rollback production release playbook sequence", adapter.CapabilityScope{}, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(playbook.Candidates) == 0 || playbook.Candidates[0].Capability.Identity.Kind != capability.KindPlaybook {
			t.Fatalf("playbook discovery = %+v", playbook.Candidates)
		}
		legacy, err := client.Search(ctx, "production release verification rollout central skill", 5)
		if err != nil || len(legacy.Candidates) == 0 {
			t.Fatalf("v1 search_skills compatibility: result=%+v err=%v", legacy, err)
		}
	})

	t.Run("Journey_B_organisational_knowledge", func(t *testing.T) {
		searchResult := callKnowledgeSearch(t, ctx, session, "renewal objections", 5)
		if len(searchResult.Results) == 0 {
			t.Fatal("OKF knowledge search returned no results")
		}
		var selected *struct {
			Result   knowledge.Result   `json:"result"`
			Metadata knowledge.Metadata `json:"metadata"`
		}
		for i := range searchResult.Results {
			if searchResult.Results[i].Metadata.Title == "Renewal objection policy" {
				selected = &searchResult.Results[i]
				break
			}
		}
		if selected == nil || selected.Result.SourceRevision != "knowledge-v1" || len(selected.Metadata.Sources) == 0 || selected.Metadata.Generated == nil || selected.Metadata.Status == "" {
			t.Fatalf("knowledge provenance/freshness metadata = %+v", selected)
		}
		read := callKnowledgeRead(t, ctx, session, selected.Result.ChunkID)
		if read.Chunk.DocumentID != selected.Result.DocumentID || read.Chunk.Content == "" {
			t.Fatalf("progressive knowledge read = %+v", read)
		}
		if backlinks := callBacklinks(t, ctx, session, selected.Result.DocumentID); len(backlinks) == 0 {
			t.Fatal("expected explicit backlink")
		}
		plain := callKnowledgeSearch(t, ctx, session, "amber-lantern-knowledge-only", 5)
		if len(plain.Results) == 0 {
			t.Fatal("plain Markdown knowledge was not indexed with OKF bundle")
		}
		capabilityLeak, err := client.SearchCapabilities(ctx, "amber-lantern-knowledge-only", adapter.CapabilityScope{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(capabilityLeak.Candidates) != 0 {
			t.Fatalf("knowledge content entered capability ranking: %+v", capabilityLeak.Candidates)
		}
	})

	t.Run("Journey_C_governance_and_reproducibility", func(t *testing.T) {
		results, err := client.SearchCapabilities(ctx, "legacy release workflow retained explicit selection", adapter.CapabilityScope{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		var deprecatedFound bool
		for _, result := range results.Candidates {
			if result.Capability.Identity.ID == "demo/central/legacy-release" {
				deprecatedFound = true
				if result.Capability.Status != capability.StatusDeprecated || result.Capability.Governance.ReplacedBy != "demo/central/release" {
					t.Fatalf("deprecated replacement guidance = %+v", result.Capability)
				}
			}
			if result.Capability.Identity.ID == "demo/central/release" && result.CandidateID == "" {
				t.Fatal("active replacement lost candidate identity")
			}
		}
		if !deprecatedFound {
			t.Fatalf("deprecated capability not surfaced: %+v", results.Candidates)
		}
		yankedSearch, err := client.SearchCapabilities(ctx, "withdrawn unsafe release workflow", adapter.CapabilityScope{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range yankedSearch.Candidates {
			if result.Capability.Identity.ID == "demo/central/withdrawn-release" {
				t.Fatalf("yanked capability selected by normal discovery: %+v", result)
			}
		}
		var yankedRevision string
		for _, doc := range skillDocs {
			if doc.SkillID == "demo/central/withdrawn-release" {
				yankedRevision = doc.ID
				break
			}
		}
		if yankedRevision == "" {
			t.Fatal("retained yanked revision missing from catalogue")
		}
		info, err := catalog.Revision(ctx, "demo", yankedRevision)
		if err != nil {
			t.Fatal(err)
		}
		restorer := &restore.Restorer{OrganizationID: "demo", Catalogue: catalog, Packages: packages, PackageSigner: packageurl.Signer{Key: []byte("m1-package-key")}, PublicBaseURL: server.URL}
		locked := lockfile.Entry{
			Name: info.Name,
			Source: lockfile.Source{Type: "local", RepositoryID: info.RepositoryID, RepositoryURL: info.RepositoryURL, Path: info.Path},
			Resolved: lockfile.Resolved{Commit: info.Commit, Tree: info.Tree},
			Integrity: lockfile.Integrity{Algorithm: "sha256", Archive: info.ArchiveSHA256TarGZ, Format: "tar.gz"},
		}
		restored, err := restorer.Restore(ctx, lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{info.SkillID: locked}})
		if err != nil {
			t.Fatal(err)
		}
		if len(restored) != 1 || restored[0].Revision.RevisionID != yankedRevision || restored[0].Digest != info.ArchiveSHA256TarGZ || restored[0].Revision.Commit != info.Commit || restored[0].Revision.Tree != info.Tree {
			t.Fatalf("exact retained restore = %+v", restored)
		}
	})

	t.Run("Journey_D_learning_loop_is_review_only", func(t *testing.T) {
		central, err := client.SearchCapabilities(ctx, "production release verification rollout central skill", adapter.CapabilityScope{}, 5)
		if err != nil {
			t.Fatal(err)
		}
		candidateResult := findCapabilityCandidate(t, central, "central")
		described, err := client.DescribeCapability(ctx, candidateResult.CandidateID, adapter.CapabilityScope{})
		if err != nil {
			t.Fatal(err)
		}
		materializationID := "m1-learning-materialization"
		if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{
			"skill_id": described.Detail.Descriptor.Identity.ID,
			"revision_id": described.Detail.Descriptor.Provenance.RevisionID,
			"archive_sha256": described.Detail.Descriptor.Provenance.ArchiveSHA256TarGZ,
			"request_id": materializationID,
		}); err != nil {
			t.Fatal(err)
		}
		lifecycle := map[string]any{
			"revision_id": described.Detail.Descriptor.Provenance.RevisionID,
			"skill_id": described.Detail.Descriptor.Identity.ID,
			"commit": described.Detail.Descriptor.Provenance.Commit,
			"tree": described.Detail.Descriptor.Provenance.Tree,
			"archive_sha256": described.Detail.Descriptor.Provenance.ArchiveSHA256TarGZ,
			"materialization_id": materializationID,
		}
		callEvidenceTool(t, ctx, session, "report_skill_feedback", map[string]any{
			"lifecycle": lifecycle, "category": "workaround_required", "summary": "Release checklist needed one extra local verification flag.", "correlation_id": "m1-run-1", "source": "m1-fixture",
		})
		callEvidenceTool(t, ctx, session, "report_skill_lifecycle", map[string]any{
			"lifecycle": lifecycle, "event": "failed", "correlation_id": "m1-run-2", "source": "m1-fixture",
		})
		var activeBefore string
		if err := db.QueryRowContext(ctx, `SELECT active_revision_id FROM skills WHERE id=?`, described.Detail.Descriptor.Identity.ID).Scan(&activeBefore); err != nil {
			t.Fatal(err)
		}
		result := callEvidenceTool(t, ctx, session, "list_improvement_candidates", map[string]any{"revision_id": described.Detail.Descriptor.Provenance.RevisionID, "limit": 25})
		var output struct {
			Candidates []evidence.Candidate `json:"candidates"`
			Total      int                  `json:"total"`
		}
		decodeEvidenceStructured(t, result.StructuredContent, &output)
		if output.Total == 0 || len(output.Candidates) == 0 {
			t.Fatalf("no reviewable improvement candidate derived: %+v", output)
		}
		for _, improvement := range output.Candidates {
			if improvement.Provenance.RevisionID != described.Detail.Descriptor.Provenance.RevisionID || len(improvement.Evidence) == 0 || improvement.Handoff.Kind != "github_issue_draft" {
				t.Fatalf("improvement candidate lost evidence traceability: %+v", improvement)
			}
		}
		var activeAfter string
		if err := db.QueryRowContext(ctx, `SELECT active_revision_id FROM skills WHERE id=?`, described.Detail.Descriptor.Identity.ID).Scan(&activeAfter); err != nil {
			t.Fatal(err)
		}
		if activeAfter != activeBefore {
			t.Fatalf("learning loop mutated active source revision: %s -> %s", activeBefore, activeAfter)
		}
	})

	t.Run("Journey_E_degraded_and_failure_modes", func(t *testing.T) {
		_, degraded, err := capabilityIndex.Search("repository A deployment migration release verification", 50, 50, 5, 60)
		if err != nil {
			t.Fatal(err)
		}
		if !degraded {
			t.Fatal("nil embedding backend did not report deterministic lexical fallback")
		}
		if _, err := client.SearchCapabilities(ctx, "release", adapter.CapabilityScope{Namespace: "team", Repository: "../repo-b"}, 5); err == nil {
			t.Fatal("forged/traversal scope unexpectedly accepted")
		}
		if err := os.WriteFile(filepath.Join(bundleRoot, "malformed.md"), []byte("---\ntitle: missing required type\n---\n# malformed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := knowledgeService.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "org-knowledge", Root: bundleRoot, Locator: "git://fixture/org-knowledge", Revision: "knowledge-bad"}}); err == nil {
			t.Fatal("malformed OKF update unexpectedly succeeded")
		}
		preserved := callKnowledgeSearch(t, ctx, session, "renewal objections", 5)
		if len(preserved.Results) == 0 || preserved.Results[0].Result.SourceRevision != "knowledge-v1" {
			t.Fatalf("failed knowledge reconciliation corrupted last-good state: %+v", preserved.Results)
		}
		if _, err := os.Stat(filepath.Join(centralRoot, "malformed", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		for _, doc := range skillDocs {
			if strings.Contains(doc.Name, "malformed") {
				t.Fatalf("malformed quarantined skill entered routing index: %+v", doc)
			}
		}
		encoded, err := json.Marshal(centralTools.Details)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(encoded)), "execution_supported\":true") {
			t.Fatal("metadata-only MCP tools gained execution authority")
		}
	})
}

func syncM1Repository(t *testing.T, ctx context.Context, sourceRoot, repositoryID string, catalog *catalogue.Store, packages *packagestore.Store) ingest.Result {
	t.Helper()
	source, err := gitstore.NewLocalSource(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := source.Fetch(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	repo := catalogue.Repository{ID: repositoryID, OrganizationID: "demo", URL: "file://" + filepath.ToSlash(sourceRoot), Ref: "local", TrustLevel: "approved", Owner: "verification"}
	result, err := ingest.SyncAtCommitWithOptions(ctx, source, repo, packages, catalog, commit, ingest.Options{Include: []string{"**/SKILL.md"}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
