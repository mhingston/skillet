package fitness

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestScopedEvidenceIsDeterministicAndVersionBound(t *testing.T) {
	ctx := context.Background()
	catalog, champion, _ := fitnessFixture(t, ctx)
	fitnessStore, err := New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	input := RecordEvidenceInput{
		OrganizationID: "demo",
		ActorID:        "runner-1",
		CorrelationID:  "fitness-run-1",
		RevisionID:     champion.ID,
		Scope: EvalScope{
			EvalSuiteID: "release-evals", EvalSuiteVersion: "v7",
			TaskDistributionID: "retention-calls", TaskDistributionVersion: "2026-09-14",
		},
		Executor: ExecutionIdentity{Agent: "codex", Model: "gpt-5.6", Harness: "eval-harness@4", Toolchain: "go1.25"},
		Run: RunMetadata{RunID: "run-champion-001", Seed: "42", SampleCount: 200, SampleSetSHA256: strings.Repeat("a", 64)},
		Metrics: []Metric{
			{Kind: MetricRuntime, Name: "p95_ms", Value: 980, Unit: "ms", Uncertainty: &Uncertainty{Method: "bootstrap", Confidence: 0.95, Lower: 960, Upper: 1000}},
			{Kind: MetricQuality, Name: "pass_rate", Value: 0.82, Unit: "ratio", Uncertainty: &Uncertainty{Method: "wilson", Confidence: 0.95, Lower: 0.79, Upper: 0.85}, EvidenceRef: "artifact:quality.json"},
			{Kind: MetricCost, Name: "cost_per_task", Value: 125000, Unit: "microunits", EvidenceRef: "artifact:cost.json"},
		},
		Provenance: []ProvenanceReference{{Kind: "report", Reference: "artifact:report.json", SHA256: strings.Repeat("b", 64)}},
	}
	first, err := fitnessStore.RecordEvidence(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := input
	secondInput.Metrics = []Metric{input.Metrics[2], input.Metrics[0], input.Metrics[1]}
	secondInput.ActorID = "runner-2"
	secondInput.CorrelationID = "retry"
	second, err := fitnessStore.RecordEvidence(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("same immutable evidence payload produced different ids: %s != %s", first.ID, second.ID)
	}
	if first.Revision.RevisionID != champion.ID || first.Revision.CapabilityID != champion.SkillID || first.Revision.Commit == "" || first.Revision.Tree == "" {
		t.Fatalf("evidence lost exact revision provenance: %+v", first.Revision)
	}
	if first.Scope.TaskDistributionVersion != "2026-09-14" || first.Executor.Model != "gpt-5.6" || first.Run.Seed != "42" || first.Run.SampleCount != 200 {
		t.Fatalf("evidence lost scope/executor/run metadata: %+v", first)
	}
	if len(first.Metrics) != 3 || first.Metrics[0].Kind != MetricCost || first.Metrics[1].Kind != MetricQuality || first.Metrics[2].Kind != MetricRuntime {
		t.Fatalf("metrics were not retained as separate deterministic dimensions: %+v", first.Metrics)
	}
	if first.Metrics[1].Uncertainty == nil || first.Metrics[1].Uncertainty.Confidence != 0.95 {
		t.Fatalf("quality uncertainty was not retained: %+v", first.Metrics[1])
	}

	changedScope := input
	changedScope.Scope.TaskDistributionVersion = "2026-09-15"
	changedScope.Run.RunID = "run-champion-002"
	changedScope.Run.SampleSetSHA256 = strings.Repeat("c", 64)
	changed, err := fitnessStore.RecordEvidence(ctx, changedScope)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID {
		t.Fatal("different task-distribution version reused scoped evidence identity")
	}
}

func TestChampionChallengerGatePassFailInconclusiveAndScopeMismatch(t *testing.T) {
	ctx := context.Background()
	catalog, championRevision, challengerRevision := fitnessFixture(t, ctx)
	fitnessStore, err := New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	scope := EvalScope{EvalSuiteID: "release-evals", EvalSuiteVersion: "v7", TaskDistributionID: "retention-calls", TaskDistributionVersion: "dist-v3"}
	policy, err := fitnessStore.CreatePolicy(ctx, CreatePolicyInput{
		OrganizationID: "demo", ActorID: "quality-owner", ChampionRevisionID: championRevision.ID, Scope: scope,
		Criteria: []Criterion{
			{MetricKind: MetricQuality, MetricName: "pass_rate", Comparator: ComparatorDeltaGTE, Threshold: 0.02, Protected: true, MinimumConfidence: 0.95},
			{MetricKind: MetricRegression, MetricName: "safety_failure_rate", Comparator: ComparatorChallengerLTE, Threshold: 0.05, Protected: true, MinimumConfidence: 0.95},
			{MetricKind: MetricRuntime, MetricName: "p95_ms", Comparator: ComparatorDeltaLTE, Threshold: -50, MinimumConfidence: 0.95},
			{MetricKind: MetricCost, MetricName: "cost_per_task", Comparator: ComparatorDeltaLTE, Threshold: 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	changedPolicy, err := fitnessStore.CreatePolicy(ctx, CreatePolicyInput{
		OrganizationID: "demo", ActorID: "quality-owner", ChampionRevisionID: championRevision.ID, Scope: scope,
		Criteria: []Criterion{
			{MetricKind: MetricQuality, MetricName: "pass_rate", Comparator: ComparatorDeltaGTE, Threshold: 0.03, Protected: true, MinimumConfidence: 0.95},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changedPolicy.ID == policy.ID || changedPolicy.PolicyRevision == policy.PolicyRevision {
		t.Fatal("changed promotion thresholds reused immutable policy identity")
	}

	champion := recordFitness(t, ctx, fitnessStore, championRevision.ID, scope, "champion", strings.Repeat("1", 64), []Metric{
		{Kind: MetricQuality, Name: "pass_rate", Value: 0.80, Unit: "ratio", Uncertainty: ci(0.95, 0.79, 0.81)},
		{Kind: MetricRegression, Name: "safety_failure_rate", Value: 0.02, Unit: "ratio", Uncertainty: ci(0.95, 0.015, 0.025)},
		{Kind: MetricRuntime, Name: "p95_ms", Value: 1000, Unit: "ms", Uncertainty: ci(0.95, 990, 1010)},
		{Kind: MetricCost, Name: "cost_per_task", Value: 100, Unit: "microunits"},
	})
	passing := recordFitness(t, ctx, fitnessStore, challengerRevision.ID, scope, "passing", strings.Repeat("2", 64), []Metric{
		{Kind: MetricQuality, Name: "pass_rate", Value: 0.86, Unit: "ratio", Uncertainty: ci(0.95, 0.85, 0.87)},
		{Kind: MetricRegression, Name: "safety_failure_rate", Value: 0.02, Unit: "ratio", Uncertainty: ci(0.95, 0.015, 0.025)},
		{Kind: MetricRuntime, Name: "p95_ms", Value: 900, Unit: "ms", Uncertainty: ci(0.95, 890, 910)},
		{Kind: MetricCost, Name: "cost_per_task", Value: 90, Unit: "microunits"},
	})
	passResult, err := fitnessStore.Compare(ctx, CompareInput{OrganizationID: "demo", ActorID: "reviewer", PolicyID: policy.ID, ChampionEvidenceID: champion.ID, ChallengerEvidenceID: passing.ID})
	if err != nil {
		t.Fatal(err)
	}
	if passResult.Result.Decision != DecisionPassesGate {
		t.Fatalf("clearly better challenger did not pass: %+v", passResult.Result)
	}

	regressing := recordFitness(t, ctx, fitnessStore, challengerRevision.ID, scope, "regressing", strings.Repeat("3", 64), []Metric{
		{Kind: MetricQuality, Name: "pass_rate", Value: 0.88, Unit: "ratio", Uncertainty: ci(0.95, 0.87, 0.89)},
		{Kind: MetricRegression, Name: "safety_failure_rate", Value: 0.08, Unit: "ratio", Uncertainty: ci(0.95, 0.07, 0.09)},
		{Kind: MetricRuntime, Name: "p95_ms", Value: 850, Unit: "ms", Uncertainty: ci(0.95, 840, 860)},
		{Kind: MetricCost, Name: "cost_per_task", Value: 70, Unit: "microunits"},
	})
	failResult, err := fitnessStore.Compare(ctx, CompareInput{OrganizationID: "demo", ActorID: "reviewer", PolicyID: policy.ID, ChampionEvidenceID: champion.ID, ChallengerEvidenceID: regressing.ID})
	if err != nil {
		t.Fatal(err)
	}
	if failResult.Result.Decision != DecisionFailsGate {
		t.Fatalf("protected regression was compensated by other improvements: %+v", failResult.Result)
	}
	var sawProtectedFailure bool
	for _, criterion := range failResult.Result.Criteria {
		if criterion.Protected && criterion.MetricKind == MetricRegression && criterion.Status == CriterionFail {
			sawProtectedFailure = true
		}
	}
	if !sawProtectedFailure {
		t.Fatalf("protected regression failure was not preserved at criterion level: %+v", failResult.Result.Criteria)
	}

	noisy := recordFitness(t, ctx, fitnessStore, challengerRevision.ID, scope, "noisy", strings.Repeat("4", 64), []Metric{
		{Kind: MetricQuality, Name: "pass_rate", Value: 0.83, Unit: "ratio", Uncertainty: ci(0.95, 0.79, 0.87)},
		{Kind: MetricRegression, Name: "safety_failure_rate", Value: 0.02, Unit: "ratio", Uncertainty: ci(0.95, 0.015, 0.025)},
		{Kind: MetricRuntime, Name: "p95_ms", Value: 900, Unit: "ms", Uncertainty: ci(0.95, 890, 910)},
		{Kind: MetricCost, Name: "cost_per_task", Value: 90, Unit: "microunits"},
	})
	inconclusive, err := fitnessStore.Compare(ctx, CompareInput{OrganizationID: "demo", ActorID: "reviewer", PolicyID: policy.ID, ChampionEvidenceID: champion.ID, ChallengerEvidenceID: noisy.ID})
	if err != nil {
		t.Fatal(err)
	}
	if inconclusive.Result.Decision != DecisionInconclusive {
		t.Fatalf("small/noisy difference was not inconclusive: %+v", inconclusive.Result)
	}

	mismatchedScope := scope
	mismatchedScope.TaskDistributionVersion = "dist-v4"
	mismatched := recordFitness(t, ctx, fitnessStore, challengerRevision.ID, mismatchedScope, "mismatch", strings.Repeat("5", 64), passing.Metrics)
	if _, err := fitnessStore.Compare(ctx, CompareInput{OrganizationID: "demo", ActorID: "reviewer", PolicyID: policy.ID, ChampionEvidenceID: champion.ID, ChallengerEvidenceID: mismatched.ID}); err == nil || !strings.Contains(err.Error(), "does not exactly match") {
		t.Fatalf("suite/task-distribution mismatch did not fail closed: %v", err)
	}
}

func recordFitness(t *testing.T, ctx context.Context, fitnessStore *Store, revisionID string, scope EvalScope, runID, digest string, metrics []Metric) Evidence {
	t.Helper()
	item, err := fitnessStore.RecordEvidence(ctx, RecordEvidenceInput{
		OrganizationID: "demo", ActorID: "runner", RevisionID: revisionID, Scope: scope,
		Executor: ExecutionIdentity{Agent: "eval-agent", Model: "gpt-5.6", Harness: "harness@1", Toolchain: "toolchain@1"},
		Run: RunMetadata{RunID: runID, Seed: "42", SampleCount: 500, SampleSetSHA256: digest}, Metrics: metrics,
		Provenance: []ProvenanceReference{{Kind: "report", Reference: "artifact:" + runID + ".json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func ci(confidence, lower, upper float64) *Uncertainty {
	return &Uncertainty{Method: "bootstrap", Confidence: confidence, Lower: lower, Upper: upper}
}

func fitnessFixture(t *testing.T, ctx context.Context) (*catalogue.Store, catalogue.Revision, catalogue.Revision) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	packages := packagestore.New(filepath.Join(root, "packages"))
	tarDigest, err := packages.Put("tar.gz", []byte("tar"))
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := packages.Put("zip", []byte("zip"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogue.New(db, packages)
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "platform-team"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "fitness fixture"}}
	champion, err := catalog.Admit(ctx, repo, skill, "commit-fitness-a", "tree-fitness-a", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	challenger, err := catalog.Admit(ctx, repo, skill, "commit-fitness-b", "tree-fitness-b", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	return catalog, champion, challenger
}
