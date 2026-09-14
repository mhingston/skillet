package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/evidence"
	"github.com/mhingston/skillet/internal/experiment"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packagebuilder"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/proposal"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestM41OptInImprovementExperimentWorkflow(t *testing.T) {
	t.Setenv("SKILLET_IMPROVEMENT_EXPERIMENTS", "true")
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)
	tarDigest, zipDigest := putM41Package(t, packages)
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "platform-team"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "release experiment workflow"}}
	first, err := catalog.Admit(ctx, repo, skill, "commit-m41-1", "tree-m41-1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	info, err := catalog.Revision(ctx, "demo", first.ID)
	if err != nil {
		t.Fatal(err)
	}

	candidateEvidence := evidence.Candidate{
		ID:                 "cand_m41_release",
		StableCapabilityID: info.SkillID,
		Category:           "workaround_required",
		Polarity:           evidence.PolarityFriction,
		Summary:            "Release verification needed one additional bounded check.",
		Provenance: evidence.RevisionProvenance{
			StableCapabilityID: info.SkillID,
			RevisionID:         info.RevisionID,
			Commit:             info.Commit,
			Tree:               info.Tree,
			ArchiveSHA256TarGZ: info.ArchiveSHA256TarGZ,
			ArchiveSHA256ZIP:   info.ArchiveSHA256ZIP,
		},
		Evidence: []evidence.EvidenceReference{{
			Kind: "feedback", ID: 41, Signal: "workaround_required", RevisionID: info.RevisionID,
			ArchiveSHA256: info.ArchiveSHA256TarGZ, MaterializationID: "mat_m41",
			SummaryExcerpt: "api_key=fixture-secret-that-must-not-leak", SummarySHA256: strings.Repeat("a", 64),
		}},
	}
	proposals, err := proposal.New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := proposals.Prepare(ctx, proposal.PrepareInput{
		OrganizationID: "demo", ActorID: "reviewer", Candidate: candidateEvidence,
		IntendedOutcome: "Improve protected release quality without weakening existing regression gates.",
	})
	if err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"diff --git a/release/SKILL.md b/release/SKILL.md",
		"--- a/release/SKILL.md",
		"+++ b/release/SKILL.md",
		"@@ -7 +7 @@",
		"-Run the documented release verification.",
		"+Run the documented release verification and bounded check.",
	}, "\n")
	ready, err := proposals.Attach(ctx, proposal.AttachInput{
		OrganizationID: "demo", ActorID: "reviewer", ProposalID: prepared.ID, BaseRevisionID: info.RevisionID,
		Patch: patch,
		Results: []proposal.VerificationResult{
			{Name: "source_ingestion_validation", Command: "verify source", Passed: true, ExitCode: 0, Summary: "passed"},
			{Name: "relevant_regression_evals", Command: "go test ./...", Passed: true, ExitCode: 0, Summary: "passed"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != proposal.StatusReadyForReview {
		t.Fatalf("proposal status=%q, want ready_for_review", ready.Status)
	}

	before := m41CanonicalState(t, ctx, catalog, info.SkillID)
	app := httpserver.NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("m41-candidate-key")}, packages, packageurl.Signer{Key: []byte("m41-package-key")}, catalog, "http://example.invalid")
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "m41-experiment-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m41HasTool(tools.Tools, "create_improvement_experiment") || !m41HasTool(tools.Tools, "submit_improvement_experiment_result") {
		t.Fatalf("opt-in experiment tools missing: %+v", tools.Tools)
	}

	createArgs := map[string]any{
		"proposal_id": ready.ID,
		"hypothesis": "The candidate raises protected release quality while preserving the latency gate.",
		"intended_outcome": "Pass rate >= 0.90 and p95 latency <= 2.0 seconds.",
		"executor": map[string]any{"agent": "codex", "model": "gpt-5.6", "harness": "m41-fixture@1", "toolchain": "go1.26"},
		"eval_suite": map[string]any{
			"id": "release-evals", "version": "v1",
			"protected": []any{
				map[string]any{"name": "quality", "metric": "pass_rate", "comparator": "gte", "threshold": 0.90},
				map[string]any{"name": "latency", "metric": "p95_seconds", "comparator": "lte", "threshold": 2.0},
			},
		},
		"budget": map[string]any{"currency": "GBP", "max_cost_microunits": 1_500_000, "max_runtime_seconds": 600, "max_input_tokens": 100000, "max_output_tokens": 20000},
		"correlation_id": "m41-create-1",
	}
	created := callM41Tool(t, ctx, session, "create_improvement_experiment", createArgs, false)
	var issued experiment.Experiment
	decodeM41Structured(t, created.StructuredContent, &issued)
	if issued.Status != experiment.StatusIssued || issued.Base.RevisionID != info.RevisionID || issued.Origin.ProposalID != ready.ID || issued.Origin.CandidateID != candidateEvidence.ID {
		t.Fatalf("issued experiment lost provenance: %+v", issued)
	}
	repeated := callM41Tool(t, ctx, session, "create_improvement_experiment", createArgs, false)
	var repeatedIssued experiment.Experiment
	decodeM41Structured(t, repeated.StructuredContent, &repeatedIssued)
	if repeatedIssued.ID != issued.ID || repeatedIssued.SpecRevision != issued.SpecRevision || repeatedIssued.HandoffSHA256 != issued.HandoffSHA256 {
		t.Fatalf("repeated handoff generation was not deterministic: first=%+v repeated=%+v", issued, repeatedIssued)
	}
	handoffJSON, err := json.Marshal(issued.Handoff)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fixture-secret-that-must-not-leak", "api_key=", "ordinary=keep-this-line", "diff --git"} {
		if strings.Contains(string(handoffJSON), forbidden) {
			t.Fatalf("experiment handoff leaked bounded proposal/source content %q: %s", forbidden, handoffJSON)
		}
	}

	badResult := map[string]any{
		"experiment_id": issued.ID,
		"spec_revision": "sha256:" + strings.Repeat("0", 64),
		"handoff_sha256": issued.HandoffSHA256,
		"eval_suite_id": issued.EvalSuite.ID,
		"eval_suite_version": issued.EvalSuite.Version,
		"measurements": []any{
			map[string]any{"name": "quality", "value": 0.94},
			map[string]any{"name": "latency", "value": 1.8},
		},
	}
	callM41Tool(t, ctx, session, "submit_improvement_experiment_result", badResult, true)
	getAfterMismatch := callM41Tool(t, ctx, session, "get_improvement_experiment", map[string]any{"experiment_id": issued.ID}, false)
	var stillIssued experiment.Experiment
	decodeM41Structured(t, getAfterMismatch.StructuredContent, &stillIssued)
	if stillIssued.Status != experiment.StatusIssued || stillIssued.Result != nil {
		t.Fatalf("mismatched result changed experiment: %+v", stillIssued)
	}

	resultArgs := map[string]any{
		"experiment_id": issued.ID,
		"spec_revision": issued.SpecRevision,
		"handoff_sha256": issued.HandoffSHA256,
		"eval_suite_id": issued.EvalSuite.ID,
		"eval_suite_version": issued.EvalSuite.Version,
		"measurements": []any{
			map[string]any{"name": "quality", "value": 0.94, "evidence_ref": "sha256:" + strings.Repeat("c", 64)},
			map[string]any{"name": "latency", "value": 1.8, "evidence_ref": "https://ci.example.invalid/m41/latency.json"},
		},
		"artifacts": []any{map[string]any{"name": "report", "reference": "https://ci.example.invalid/m41/report.json", "sha256": strings.Repeat("d", 64)}},
		"summary": "The external runner completed protected evaluation.",
		"correlation_id": "m41-result-1",
	}
	completedResult := callM41Tool(t, ctx, session, "submit_improvement_experiment_result", resultArgs, false)
	var completed experiment.Experiment
	decodeM41Structured(t, completedResult.StructuredContent, &completed)
	if completed.Status != experiment.StatusCompleted || completed.Result == nil || len(completed.Result.Outcomes) != 2 {
		t.Fatalf("result intake did not complete exact bound experiment: %+v", completed)
	}
	if after := m41CanonicalState(t, ctx, catalog, info.SkillID); after != before {
		t.Fatalf("experiment completion changed canonical state: before=%+v after=%+v", before, after)
	}

	staleCreateArgs := cloneM41Args(t, createArgs)
	staleCreateArgs["hypothesis"] = "A second immutable experiment is issued before the source advances."
	staleCreated := callM41Tool(t, ctx, session, "create_improvement_experiment", staleCreateArgs, false)
	var staleIssued experiment.Experiment
	decodeM41Structured(t, staleCreated.StructuredContent, &staleIssued)
	if staleIssued.ID == issued.ID || staleIssued.SpecRevision == issued.SpecRevision {
		t.Fatalf("changed experiment did not create a new immutable spec: old=%+v new=%+v", issued, staleIssued)
	}
	if _, err := catalog.Admit(ctx, repo, skill, "commit-m41-2", "tree-m41-2", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest}); err != nil {
		t.Fatal(err)
	}
	staleResultArgs := cloneM41Args(t, resultArgs)
	staleResultArgs["experiment_id"] = staleIssued.ID
	staleResultArgs["spec_revision"] = staleIssued.SpecRevision
	staleResultArgs["handoff_sha256"] = staleIssued.HandoffSHA256
	callM41Tool(t, ctx, session, "submit_improvement_experiment_result", staleResultArgs, true)
	staleGet := callM41Tool(t, ctx, session, "get_improvement_experiment", map[string]any{"experiment_id": staleIssued.ID}, false)
	var stale experiment.Experiment
	decodeM41Structured(t, staleGet.StructuredContent, &stale)
	if stale.Status != experiment.StatusStale || stale.Result != nil {
		t.Fatalf("stale experiment accepted result: %+v", stale)
	}

	var issuedAudits, resultAudits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='improvement_experiment_issued'`).Scan(&issuedAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='improvement_experiment_result_recorded'`).Scan(&resultAudits); err != nil {
		t.Fatal(err)
	}
	if issuedAudits < 2 || resultAudits != 1 {
		t.Fatalf("experiment audit trace incomplete: issued=%d result=%d", issuedAudits, resultAudits)
	}
}

type m41State struct {
	ActiveRevisionID string
	Searchable       int
	Owner            string
}

func m41CanonicalState(t *testing.T, ctx context.Context, catalog *catalogue.Store, skillID string) m41State {
	t.Helper()
	var state m41State
	if err := catalog.DB.QueryRowContext(ctx, `SELECT active_revision_id, searchable, owner FROM skills WHERE id=?`, skillID).Scan(&state.ActiveRevisionID, &state.Searchable, &state.Owner); err != nil {
		t.Fatal(err)
	}
	return state
}

func putM41Package(t *testing.T, packages *packagestore.Store) (string, string) {
	t.Helper()
	skillMarkdown := "---\nname: release\ndescription: release experiment workflow\n---\n\n# Release\n\nRun the documented release verification.\n"
	notes := "api_key=fixture-secret-that-must-not-leak\nordinary=keep-this-line\n"
	contents := map[string][]byte{
		"release/SKILL.md": []byte(skillMarkdown),
		"release/notes.md": []byte(notes),
	}
	entries := []packagebuilder.Entry{
		{Path: "release/SKILL.md", Kind: packagebuilder.Regular, Mode: 0o644, Size: int64(len(skillMarkdown))},
		{Path: "release/notes.md", Kind: packagebuilder.Regular, Mode: 0o644, Size: int64(len(notes))},
	}
	built, err := packagebuilder.Build("release", "release", entries, func(path string) ([]byte, error) {
		value, ok := contents[path]
		if !ok {
			return nil, fmt.Errorf("fixture path %s not found", path)
		}
		return value, nil
	}, packagebuilder.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	tarDigest, err := packages.Put("tar.gz", built.TarGZ)
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := packages.Put("zip", built.ZIP)
	if err != nil {
		t.Fatal(err)
	}
	return tarDigest, zipDigest
}

func callM41Tool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any, wantError bool) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("tool %s IsError=%v, want %v; content=%+v", name, result.IsError, wantError, result.Content)
	}
	return result
}

func decodeM41Structured(t *testing.T, value any, out any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		t.Fatalf("decode structured content: %v; content=%s", err, encoded)
	}
}

func cloneM41Args(t *testing.T, input map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func m41HasTool(tools []*mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
