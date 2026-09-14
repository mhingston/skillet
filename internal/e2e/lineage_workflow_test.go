package e2e

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/experiment"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/lineage"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestM42OptInImprovementLineageWorkflow(t *testing.T) {
	t.Setenv("SKILLET_IMPROVEMENT_LINEAGE", "true")
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar"))
	zipDigest, _ := packages.Put("zip", []byte("zip"))
	catalog := catalogue.New(db, packages)
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "platform-team"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "lineage workflow"}}
	a, err := catalog.Admit(ctx, repo, skill, "commit-m42-a", "tree-m42-a", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	b, err := catalog.Admit(ctx, repo, skill, "commit-m42-b", "tree-m42-b", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Admit(ctx, repo, skill, "commit-m42-c", "tree-m42-c", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := experiment.New(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	insertM42Experiment(t, ctx, catalog, "exp-m42-ab", a.SkillID, a.ID, "cand-m42-b")
	insertM42Experiment(t, ctx, catalog, "exp-m42-ac", a.SkillID, a.ID, "cand-m42-c")
	insertM42Experiment(t, ctx, catalog, "exp-m42-bc", b.SkillID, b.ID, "cand-m42-c2")
	insertM42Experiment(t, ctx, catalog, "exp-m42-ca", c.SkillID, c.ID, "cand-m42-a")

	var activeBefore string
	var searchableBefore int
	if err := db.QueryRowContext(ctx, `SELECT active_revision_id, searchable FROM skills WHERE id=?`, a.SkillID).Scan(&activeBefore, &searchableBefore); err != nil {
		t.Fatal(err)
	}

	app := httpserver.NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("m42-candidate-key")}, packages, packageurl.Signer{Key: []byte("m42-package-key")}, catalog, "http://example.invalid")
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "m42-lineage-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	abResult := callM41Tool(t, ctx, session, "record_revision_lineage", map[string]any{
		"parent_revision_id": a.ID, "descendant_kind": "revision", "descendant_id": b.ID,
		"relationship": "derived_from", "experiment_ids": []any{"exp-m42-ab"}, "correlation_id": "m42-ab",
	}, false)
	var ab lineage.Entry
	decodeM41Structured(t, abResult.StructuredContent, &ab)
	if ab.Record.ParentRevisionID != a.ID || ab.Record.DescendantID != b.ID || len(ab.Experiments) != 1 || ab.Experiments[0].EvalSuiteID != "quality" {
		t.Fatalf("A -> B lineage lost experiment/eval provenance: %+v", ab)
	}

	acResult := callM41Tool(t, ctx, session, "record_revision_lineage", map[string]any{
		"parent_revision_id": a.ID, "descendant_kind": "revision", "descendant_id": c.ID,
		"relationship": "challenger_of", "experiment_ids": []any{"exp-m42-ac"}, "correlation_id": "m42-ac",
	}, false)
	var ac lineage.Entry
	decodeM41Structured(t, acResult.StructuredContent, &ac)
	decisionResult := callM41Tool(t, ctx, session, "record_lineage_decision", map[string]any{
		"lineage_id": ac.Record.ID, "state": "rejected", "reference": "review:rejected-challenger", "correlation_id": "m42-decision",
	}, false)
	decodeM41Structured(t, decisionResult.StructuredContent, &ac)
	if ac.Decision == nil || ac.Decision.State != lineage.DecisionRejected {
		t.Fatalf("rejected challenger decision missing: %+v", ac)
	}

	callM41Tool(t, ctx, session, "record_revision_lineage", map[string]any{
		"parent_revision_id": b.ID, "descendant_kind": "revision", "descendant_id": c.ID,
		"relationship": "derived_from", "experiment_ids": []any{"exp-m42-bc"},
	}, false)

	viewAResult := callM41Tool(t, ctx, session, "get_revision_lineage", map[string]any{"revision_id": a.ID, "limit": 10}, false)
	var viewA lineage.View
	decodeM41Structured(t, viewAResult.StructuredContent, &viewA)
	if len(viewA.Descendants) != 2 {
		t.Fatalf("competing A descendants=%d, want 2: %+v", len(viewA.Descendants), viewA)
	}
	var rejectedVisible bool
	for _, entry := range viewA.Descendants {
		if entry.Record.ID == ac.Record.ID && entry.Decision != nil && entry.Decision.State == lineage.DecisionRejected {
			rejectedVisible = true
		}
	}
	if !rejectedVisible {
		t.Fatal("rejected challenger was not retained in bounded lineage view")
	}

	viewBResult := callM41Tool(t, ctx, session, "get_revision_lineage", map[string]any{"revision_id": b.ID, "limit": 10}, false)
	var viewB lineage.View
	decodeM41Structured(t, viewBResult.StructuredContent, &viewB)
	if len(viewB.Parents) != 1 || len(viewB.Descendants) != 1 || viewB.Parents[0].Record.DescendantID != b.ID || viewB.Descendants[0].Record.DescendantID != c.ID {
		t.Fatalf("linear A -> B -> C view incorrect: %+v", viewB)
	}

	callM41Tool(t, ctx, session, "record_revision_lineage", map[string]any{
		"parent_revision_id": c.ID, "descendant_kind": "revision", "descendant_id": a.ID,
		"relationship": "derived_from", "experiment_ids": []any{"exp-m42-ca"},
	}, true)
	callM41Tool(t, ctx, session, "record_revision_lineage", map[string]any{
		"parent_revision_id": a.ID, "descendant_kind": "revision", "descendant_id": b.ID,
		"relationship": "depends_on", "experiment_ids": []any{"exp-m42-ab"},
	}, true)

	var activeAfter string
	var searchableAfter int
	if err := db.QueryRowContext(ctx, `SELECT active_revision_id, searchable FROM skills WHERE id=?`, a.SkillID).Scan(&activeAfter, &searchableAfter); err != nil {
		t.Fatal(err)
	}
	if activeAfter != activeBefore || searchableAfter != searchableBefore || activeAfter != c.ID {
		t.Fatalf("lineage changed canonical resolution/ranking inputs: before=(%s,%d) after=(%s,%d)", activeBefore, searchableBefore, activeAfter, searchableAfter)
	}

	otherApp := httpserver.NewComplete(nil, nil, nil, "other", candidate.Signer{Key: []byte("m42-candidate-key")}, packages, packageurl.Signer{Key: []byte("m42-package-key")}, catalog, "http://example.invalid")
	otherServer := httptest.NewServer(otherApp.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "other"}))
	defer otherServer.Close()
	otherClient := mcp.NewClient(&mcp.Implementation{Name: "m42-lineage-cross-scope", Version: "1"}, nil)
	otherSession, err := otherClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: otherServer.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer otherSession.Close()
	callM41Tool(t, ctx, otherSession, "get_revision_lineage", map[string]any{"revision_id": a.ID}, true)

	var lineageAudits, decisionAudits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='improvement_lineage_recorded'`).Scan(&lineageAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='improvement_lineage_decision_recorded'`).Scan(&decisionAudits); err != nil {
		t.Fatal(err)
	}
	if lineageAudits != 3 || decisionAudits != 1 {
		t.Fatalf("lineage audit trace incomplete: lineage=%d decisions=%d", lineageAudits, decisionAudits)
	}
}

func insertM42Experiment(t *testing.T, ctx context.Context, catalog *catalogue.Store, id, capabilityID, baseRevisionID, candidateID string) {
	t.Helper()
	_, err := catalog.DB.ExecContext(ctx, `INSERT INTO improvement_experiments(
		id, organization_id, capability_id, base_revision_id, proposal_id, candidate_id,
		proposal_snapshot_sha256, evidence_json, hypothesis, intended_outcome, executor_json,
		eval_suite_json, budget_json, spec_revision, handoff_json, handoff_sha256, actor_id
	) VALUES (?, 'demo', ?, ?, ?, ?, ?, '[]', 'hypothesis', 'outcome', '{}', ?, '{}', ?, '{}', ?, 'runner')`,
		id, capabilityID, baseRevisionID, "proposal-"+id, candidateID, "snapshot-"+id,
		`{"id":"quality","version":"v1","protected":[]}`, "spec-"+id, "handoff-"+id)
	if err != nil {
		t.Fatal(err)
	}
}
