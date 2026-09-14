package e2e

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
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
	"github.com/mhingston/skillet/internal/runner"
	"github.com/mhingston/skillet/internal/runnerfixture"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestM46ExternalImprovementRunnerProtocol(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)
	tarDigest, zipDigest := putM46Package(t, packages)
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "platform-team"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "release runner workflow"}}
	revision, err := catalog.Admit(ctx, repo, skill, "commit-m46-1", "tree-m46-1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	info, err := catalog.Revision(ctx, "demo", revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	ready := prepareM46Proposal(t, ctx, catalog, info)
	experiments, err := experiment.New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	newExperiment := func(hypothesis string) experiment.Experiment {
		t.Helper()
		item, createErr := experiments.Create(ctx, experiment.CreateInput{
			OrganizationID: "demo", ActorID: "reviewer", Proposal: ready,
			Hypothesis: hypothesis,
			IntendedOutcome: "Pass rate >= 0.90 and p95 latency <= 2.0 seconds.",
			Executor: experiment.ExecutorIdentity{Harness: "external-ci"},
			EvalSuite: experiment.EvalSuite{ID: "release-evals", Version: "v1", Protected: []experiment.ProtectedEval{
				{Name: "quality", Metric: "pass_rate", Comparator: "gte", Threshold: 0.90},
				{Name: "latency", Metric: "p95_seconds", Comparator: "lte", Threshold: 2.0},
			}},
			Budget: experiment.Budget{Currency: "GBP", MaxCostMicrounits: 1_500_000, MaxRuntimeSeconds: 600, MaxInputTokens: 100000, MaxOutputTokens: 20000},
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return item
	}
	successExperiment := newExperiment("External deterministic CI validates the candidate without widening execution authority.")
	failedExperiment := newExperiment("External deterministic CI failure remains explicit and reviewable.")
	cancelledExperiment := newExperiment("External deterministic CI cancellation remains explicit and reviewable.")

	app := httpserver.NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("m46-candidate-key")}, packages, packageurl.Signer{Key: []byte("m46-package-key")}, catalog, "http://example.invalid")

	// M4.6 is doubly opt-in: enabling the runner flag alone does not expose or
	// permit registration/dispatch while the M4.1 experiment surface is disabled.
	t.Setenv("SKILLET_IMPROVEMENT_EXPERIMENTS", "")
	t.Setenv("SKILLET_EXTERNAL_IMPROVEMENT_RUNNERS", "true")
	offServer := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	offClient := mcp.NewClient(&mcp.Implementation{Name: "m46-off", Version: "1"}, nil)
	offSession, err := offClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: offServer.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	offTools, err := offSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m41HasTool(offTools.Tools, "register_external_improvement_runner") || m41HasTool(offTools.Tools, "dispatch_improvement_experiment") {
		t.Fatalf("external runner tools exposed while M4 experiments disabled: %+v", offTools.Tools)
	}
	offSession.Close()
	offServer.Close()

	t.Setenv("SKILLET_IMPROVEMENT_EXPERIMENTS", "true")
	t.Setenv("SKILLET_EXTERNAL_IMPROVEMENT_RUNNERS", "true")
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "m46-runner-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"register_external_improvement_runner", "dispatch_improvement_experiment", "record_external_runner_status", "submit_external_runner_result", "get_external_runner_run"} {
		if !m41HasTool(tools.Tools, name) {
			t.Fatalf("missing M4.6 tool %q", name)
		}
	}

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	registerArgs := map[string]any{
		"runner_id": "deterministic-ci",
		"version": "1.0.0",
		"capabilities": []any{"repository-validation", "protected-eval"},
		"accepted_spec_versions": []any{"skillet.improvement-experiment-spec/v1"},
		"scope": map[string]any{
			"capability_ids": []any{info.SkillID},
			"max_cost_microunits": 2_000_000,
			"max_runtime_seconds": 900,
		},
		"public_key": base64.StdEncoding.EncodeToString(publicKey),
	}
	registeredResult := callM41Tool(t, ctx, session, "register_external_improvement_runner", registerArgs, false)
	var registration runner.Registration
	decodeM41Structured(t, registeredResult.StructuredContent, &registration)
	if registration.RegistrationSHA256 == "" || registration.PublicKey == "" || len(registration.Scope.CapabilityIDs) != 1 {
		t.Fatalf("runner registration lost identity/scope evidence: %+v", registration)
	}

	before := m46CanonicalState(t, ctx, catalog, info.SkillID)
	adapter := runnerfixture.DeterministicCI{PrivateKey: privateKey}
	dispatch := func(exp experiment.Experiment, correlation string) runner.Run {
		t.Helper()
		args := map[string]any{
			"experiment_id": exp.ID,
			"runner_id": registration.RunnerID,
			"runner_version": registration.Version,
			"required_capabilities": []any{"repository-validation", "protected-eval"},
			"correlation_id": correlation,
		}
		result := callM41Tool(t, ctx, session, "dispatch_improvement_experiment", args, false)
		var item runner.Run
		decodeM41Structured(t, result.StructuredContent, &item)
		repeated := callM41Tool(t, ctx, session, "dispatch_improvement_experiment", args, false)
		var repeatedItem runner.Run
		decodeM41Structured(t, repeated.StructuredContent, &repeatedItem)
		if item.ID != repeatedItem.ID || item.DispatchSHA256 != repeatedItem.DispatchSHA256 || item.Dispatch.IdempotencyKey != repeatedItem.Dispatch.IdempotencyKey {
			t.Fatalf("runner dispatch was not deterministic/idempotent: first=%+v repeated=%+v", item, repeatedItem)
		}
		if item.Dispatch.HandoffSHA256 != exp.HandoffSHA256 || item.Dispatch.SpecRevision != exp.SpecRevision || item.Dispatch.ExperimentID != exp.ID {
			t.Fatalf("runner dispatch lost exact experiment binding: %+v", item.Dispatch)
		}
		return item
	}
	recordStart := func(runItem runner.Run) {
		t.Helper()
		accepted, signErr := adapter.Accepted(runItem.Dispatch, runItem.DispatchSHA256)
		if signErr != nil {
			t.Fatal(signErr)
		}
		tampered := accepted
		tampered.Message = "tampered after signature"
		callM41Tool(t, ctx, session, "record_external_runner_status", map[string]any{"event": m46Map(t, tampered)}, true)
		callM41Tool(t, ctx, session, "record_external_runner_status", map[string]any{"event": m46Map(t, accepted)}, false)
		// Exact replay is idempotent; a conflicting replay above failed closed.
		callM41Tool(t, ctx, session, "record_external_runner_status", map[string]any{"event": m46Map(t, accepted)}, false)
		running, signErr := adapter.Running(runItem.Dispatch, runItem.DispatchSHA256)
		if signErr != nil {
			t.Fatal(signErr)
		}
		callM41Tool(t, ctx, session, "record_external_runner_status", map[string]any{"event": m46Map(t, running)}, false)
	}

	successRun := dispatch(successExperiment, "m46-success")
	recordStart(successRun)
	validResult, err := adapter.Complete(successRun.Dispatch, successRun.DispatchSHA256, []runnerfixture.ValidationMeasurement{
		{Name: "quality", Value: 0.95, EvidenceRef: "sha256:" + strings.Repeat("a", 64)},
		{Name: "latency", Value: 1.7, EvidenceRef: "https://ci.example.invalid/m46/latency.json"},
	}, []experiment.ArtifactReference{{Name: "report", Reference: "https://ci.example.invalid/m46/report.json", SHA256: strings.Repeat("b", 64)}},
		[]experiment.ArtifactReference{{Name: "log", Reference: "https://ci.example.invalid/m46/log.txt", SHA256: strings.Repeat("c", 64)}},
		runner.ResourceUsage{Currency: "GBP", CostMicrounits: 1_600_000, RuntimeSeconds: 500, InputTokens: 90000, OutputTokens: 18000})
	if err != nil {
		t.Fatal(err)
	}
	mismatched := validResult
	mismatched.SpecRevision = "sha256:" + strings.Repeat("0", 64)
	mismatched.Signature = ""
	mismatched, err = runner.SignResult(privateKey, mismatched)
	if err != nil {
		t.Fatal(err)
	}
	callM41Tool(t, ctx, session, "submit_external_runner_result", map[string]any{"result": m46Map(t, mismatched)}, true)
	completedResult := callM41Tool(t, ctx, session, "submit_external_runner_result", map[string]any{"result": m46Map(t, validResult)}, false)
	var completed externalRunnerResultForM46
	decodeM41Structured(t, completedResult.StructuredContent, &completed)
	if completed.Run.State != runner.StateSucceeded || completed.Experiment.Status != experiment.StatusCompleted || completed.Experiment.Result == nil || len(completed.Experiment.Result.Outcomes) != 2 {
		t.Fatalf("successful signed runner result did not complete exact experiment: %+v", completed)
	}
	if completed.Run.BudgetAssessment == nil || !completed.Run.BudgetAssessment.CostExceeded {
		t.Fatalf("Skillet did not preserve/derive runner budget evidence: %+v", completed.Run)
	}
	// Exact terminal replay is safe and idempotent.
	replay := callM41Tool(t, ctx, session, "submit_external_runner_result", map[string]any{"result": m46Map(t, validResult)}, false)
	var replayed externalRunnerResultForM46
	decodeM41Structured(t, replay.StructuredContent, &replayed)
	if replayed.Run.ResultSHA256 != completed.Run.ResultSHA256 {
		t.Fatalf("exact result replay changed evidence identity: first=%s replay=%s", completed.Run.ResultSHA256, replayed.Run.ResultSHA256)
	}

	failedRun := dispatch(failedExperiment, "m46-failed")
	recordStart(failedRun)
	failedEnvelope, err := adapter.Failed(failedRun.Dispatch, failedRun.DispatchSHA256, "bounded validation process reported failure", runner.ResourceUsage{RuntimeSeconds: 22})
	if err != nil {
		t.Fatal(err)
	}
	failedResult := callM41Tool(t, ctx, session, "submit_external_runner_result", map[string]any{"result": m46Map(t, failedEnvelope)}, false)
	var failed externalRunnerResultForM46
	decodeM41Structured(t, failedResult.StructuredContent, &failed)
	if failed.Run.State != runner.StateFailed || failed.Experiment.Status != experiment.StatusFailed || failed.Experiment.Result == nil || failed.Experiment.Result.FailureReason == "" {
		t.Fatalf("failed external run was not explicit/reviewable: %+v", failed)
	}

	cancelledRun := dispatch(cancelledExperiment, "m46-cancelled")
	recordStart(cancelledRun)
	cancelledEnvelope, err := adapter.Cancelled(cancelledRun.Dispatch, cancelledRun.DispatchSHA256, runner.ResourceUsage{RuntimeSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	cancelledResult := callM41Tool(t, ctx, session, "submit_external_runner_result", map[string]any{"result": m46Map(t, cancelledEnvelope)}, false)
	var cancelled externalRunnerResultForM46
	decodeM41Structured(t, cancelledResult.StructuredContent, &cancelled)
	if cancelled.Run.State != runner.StateCancelled || cancelled.Experiment.Status != experiment.StatusCancelled {
		t.Fatalf("cancelled external run was not explicit/reviewable: %+v", cancelled)
	}

	if after := m46CanonicalState(t, ctx, catalog, info.SkillID); after != before {
		t.Fatalf("external runner evidence mutated canonical source/ranking/governance state: before=%+v after=%+v", before, after)
	}
	if strings.Contains(string(m46JSON(t, validResult)), "threshold") {
		t.Fatal("runner result payload unexpectedly carried protected thresholds")
	}
	var dispatchAudits, resultAudits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='external_improvement_runner_dispatched'`).Scan(&dispatchAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='external_improvement_runner_result_recorded'`).Scan(&resultAudits); err != nil {
		t.Fatal(err)
	}
	if dispatchAudits != 3 || resultAudits != 3 {
		t.Fatalf("external runner audit evidence incomplete: dispatches=%d results=%d", dispatchAudits, resultAudits)
	}
}

type externalRunnerResultForM46 struct {
	Run        runner.Run            `json:"run"`
	Experiment experiment.Experiment `json:"experiment"`
}

type m46State struct {
	ActiveRevisionID string
	Searchable       int
	Owner            string
}

func m46CanonicalState(t *testing.T, ctx context.Context, catalog *catalogue.Store, skillID string) m46State {
	t.Helper()
	var state m46State
	if err := catalog.DB.QueryRowContext(ctx, `SELECT active_revision_id, searchable, owner FROM skills WHERE id=?`, skillID).Scan(&state.ActiveRevisionID, &state.Searchable, &state.Owner); err != nil {
		t.Fatal(err)
	}
	return state
}

func prepareM46Proposal(t *testing.T, ctx context.Context, catalog *catalogue.Store, info catalogue.RevisionInfo) proposal.Proposal {
	t.Helper()
	candidateEvidence := evidence.Candidate{
		ID: "cand_m46_release", StableCapabilityID: info.SkillID,
		Category: "workaround_required", Polarity: evidence.PolarityFriction,
		Summary: "Release validation should be proven through an external deterministic runner.",
		Provenance: evidence.RevisionProvenance{
			StableCapabilityID: info.SkillID, RevisionID: info.RevisionID, Commit: info.Commit, Tree: info.Tree,
			ArchiveSHA256TarGZ: info.ArchiveSHA256TarGZ, ArchiveSHA256ZIP: info.ArchiveSHA256ZIP,
		},
		Evidence: []evidence.EvidenceReference{{Kind: "feedback", ID: 46, Signal: "workaround_required", RevisionID: info.RevisionID, ArchiveSHA256: info.ArchiveSHA256TarGZ, SummarySHA256: strings.Repeat("d", 64)}},
	}
	proposals, err := proposal.New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := proposals.Prepare(ctx, proposal.PrepareInput{OrganizationID: "demo", ActorID: "reviewer", Candidate: candidateEvidence, IntendedOutcome: "Validate a bounded candidate externally without weakening protected gates."})
	if err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"diff --git a/release/SKILL.md b/release/SKILL.md",
		"--- a/release/SKILL.md",
		"+++ b/release/SKILL.md",
		"@@ -7 +7 @@",
		"-Run release validation.",
		"+Run release validation with the bounded candidate.",
	}, "\n")
	ready, err := proposals.Attach(ctx, proposal.AttachInput{
		OrganizationID: "demo", ActorID: "reviewer", ProposalID: prepared.ID, BaseRevisionID: info.RevisionID, Patch: patch,
		Results: []proposal.VerificationResult{
			{Name: "source_ingestion_validation", Command: "verify source", Passed: true, ExitCode: 0, Summary: "passed"},
			{Name: "relevant_regression_evals", Command: "go test ./...", Passed: true, ExitCode: 0, Summary: "passed"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

func putM46Package(t *testing.T, packages *packagestore.Store) (string, string) {
	t.Helper()
	skillMarkdown := "---\nname: release\ndescription: release runner workflow\n---\n\n# Release\n\nRun release validation.\n"
	contents := map[string][]byte{"release/SKILL.md": []byte(skillMarkdown)}
	entries := []packagebuilder.Entry{{Path: "release/SKILL.md", Kind: packagebuilder.Regular, Mode: 0o644, Size: int64(len(skillMarkdown))}}
	built, err := packagebuilder.Build("release", "release", entries, func(path string) ([]byte, error) {
		value, ok := contents[path]
		if !ok {
			return nil, fmt.Errorf("fixture file not found: %s", path)
		}
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := packages.Put(built.TarGZSHA256, built.TarGZ); err != nil {
		t.Fatal(err)
	}
	if err := packages.Put(built.ZIPSHA256, built.ZIP); err != nil {
		t.Fatal(err)
	}
	return built.TarGZSHA256, built.ZIPSHA256
}

func m46Map(t *testing.T, value any) map[string]any {
	t.Helper()
	payload := m46JSON(t, value)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func m46JSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
