package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/evidence"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestEvidenceToImprovementCandidateWorkflowIsReviewOnlyAndDeterministic(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, err := packages.Put("tar.gz", []byte("tar-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := packages.Put("zip", []byte("zip-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogue.New(db, packages)
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "Build a verified implementation plan."}}
	revision, err := catalog.Admit(ctx, catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "verification"}, skill, "commit-evidence", "tree-evidence", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{
		"skill_id": revision.SkillID, "revision_id": revision.ID, "archive_sha256": tarDigest, "request_id": "materialize-evidence",
	}); err != nil {
		t.Fatal(err)
	}

	app := httpserver.NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("candidate-test-key")}, packages, packageurl.Signer{Key: []byte("package-test-key")}, catalog, "http://example.invalid")
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "evidence-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	lifecycle := map[string]any{
		"revision_id": revision.ID,
		"skill_id": revision.SkillID,
		"commit": "commit-evidence",
		"tree": "tree-evidence",
		"archive_sha256": tarDigest,
		"materialization_id": "materialize-evidence",
	}
	for _, feedback := range []map[string]any{
		{"lifecycle": lifecycle, "category": "workaround_required", "summary": "The documented command required an extra environment flag.", "correlation_id": "run-1", "source": "fixture"},
		{"lifecycle": lifecycle, "category": "workaround_required", "summary": "The documented command required an extra environment flag.", "correlation_id": "run-1", "source": "fixture-copy"},
		{"lifecycle": lifecycle, "category": "workaround_required", "summary": "The documented command required an extra environment flag.", "correlation_id": "run-2", "source": "fixture"},
		{"lifecycle": lifecycle, "category": "effective_pattern", "summary": "Digest verification prevented stale output from being activated.", "correlation_id": "run-3", "source": "fixture"},
	} {
		callEvidenceTool(t, ctx, session, "report_skill_feedback", feedback)
	}
	callEvidenceTool(t, ctx, session, "report_skill_lifecycle", map[string]any{
		"lifecycle": lifecycle, "event": "failed", "correlation_id": "run-4", "source": "fixture",
	})

	var auditBefore, feedbackBefore int
	var activeBefore string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_feedback`).Scan(&feedbackBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT active_revision_id FROM skills WHERE id=?`, revision.SkillID).Scan(&activeBefore); err != nil {
		t.Fatal(err)
	}

	first := callEvidenceTool(t, ctx, session, "list_improvement_candidates", map[string]any{"revision_id": revision.ID, "limit": 25})
	second := callEvidenceTool(t, ctx, session, "list_improvement_candidates", map[string]any{"revision_id": revision.ID, "limit": 25})
	type candidateOutput struct {
		Candidates                []evidence.Candidate `json:"candidates"`
		Total                     int                  `json:"total"`
		DuplicatesIgnored         int                  `json:"duplicates_ignored"`
		FeedbackEvidenceIncluded  int                  `json:"feedback_evidence_included"`
		LifecycleEvidenceIncluded int                  `json:"lifecycle_evidence_included"`
		SourceEvidenceTruncated   bool                 `json:"source_evidence_truncated"`
	}
	var firstOut, secondOut candidateOutput
	decodeEvidenceStructured(t, first.StructuredContent, &firstOut)
	decodeEvidenceStructured(t, second.StructuredContent, &secondOut)
	if firstOut.Total != 3 || len(firstOut.Candidates) != 3 || firstOut.DuplicatesIgnored != 1 || firstOut.FeedbackEvidenceIncluded != 4 || firstOut.LifecycleEvidenceIncluded != 1 || firstOut.SourceEvidenceTruncated {
		t.Fatalf("candidate output = %+v", firstOut)
	}
	firstJSON, _ := json.Marshal(firstOut)
	secondJSON, _ := json.Marshal(secondOut)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("candidate derivation is not deterministic:\n%s\n%s", firstJSON, secondJSON)
	}

	byCategory := map[string]evidence.Candidate{}
	for _, improvement := range firstOut.Candidates {
		byCategory[improvement.Category] = improvement
		if improvement.Provenance.RevisionID != revision.ID || improvement.Provenance.StableCapabilityID != revision.SkillID || improvement.Provenance.Commit != "commit-evidence" || improvement.Provenance.Tree != "tree-evidence" {
			t.Fatalf("candidate lost exact revision provenance: %+v", improvement)
		}
		if len(improvement.Evidence) == 0 || improvement.Handoff.Kind != "github_issue_draft" || improvement.Handoff.BodyMarkdown == "" {
			t.Fatalf("candidate is not traceable/reviewable: %+v", improvement)
		}
		if !improvement.ContradictoryEvidencePresent {
			t.Fatalf("positive/friction contradiction was hidden: %+v", improvement)
		}
	}
	if byCategory["workaround_required"].SignalCount != 2 {
		t.Fatalf("correlated duplicate inflated workaround evidence: %+v", byCategory["workaround_required"])
	}
	if byCategory["effective_reusable_pattern"].Polarity != evidence.PolarityPositive {
		t.Fatalf("effective pattern was not retained as positive evidence: %+v", byCategory["effective_reusable_pattern"])
	}
	if byCategory["lifecycle_failure"].Polarity != evidence.PolarityFriction {
		t.Fatalf("lifecycle failure was not derived as friction evidence: %+v", byCategory["lifecycle_failure"])
	}

	var auditAfter, feedbackAfter int
	var activeAfter string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_feedback`).Scan(&feedbackAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT active_revision_id FROM skills WHERE id=?`, revision.SkillID).Scan(&activeAfter); err != nil {
		t.Fatal(err)
	}
	if auditAfter != auditBefore || feedbackAfter != feedbackBefore || activeAfter != activeBefore {
		t.Fatalf("candidate read mutated catalogue/evidence: audit %d->%d feedback %d->%d active %q->%q", auditBefore, auditAfter, feedbackBefore, feedbackAfter, activeBefore, activeAfter)
	}
}

func callEvidenceTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s returned tool error: %+v", name, result.Content)
	}
	return result
}

func decodeEvidenceStructured(t *testing.T, value any, dst any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		t.Fatal(err)
	}
}
