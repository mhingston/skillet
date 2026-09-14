package e2e

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/fitness"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestM43ScopedFitnessChampionChallengerWorkflow(t *testing.T) {
	t.Setenv("SKILLET_IMPROVEMENT_FITNESS", "true")
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
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
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "fitness workflow"}}
	championRevision, err := catalog.Admit(ctx, repo, skill, "commit-m43-a", "tree-m43-a", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	challengerRevision, err := catalog.Admit(ctx, repo, skill, "commit-m43-b", "tree-m43-b", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}

	before := m41CanonicalState(t, ctx, catalog, championRevision.SkillID)
	app := httpserver.NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("m43-candidate-key")}, packages, packageurl.Signer{Key: []byte("m43-package-key")}, catalog, "http://example.invalid")
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "m43-fitness-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	champion := recordM43Fitness(t, ctx, session, championRevision.ID, "dist-v3", "champion", strings.Repeat("1", 64), []any{
		m43Metric("quality", "pass_rate", 0.80, "ratio", 0.79, 0.81),
		m43Metric("regression", "safety_failure_rate", 0.02, "ratio", 0.015, 0.025),
		m43Metric("runtime", "p95_ms", 1000, "ms", 990, 1010),
		map[string]any{"kind": "cost", "name": "cost_per_task", "value": 100.0, "unit": "microunits", "evidence_ref": "artifact:champion-cost.json"},
	})
	policyResult := callM41Tool(t, ctx, session, "create_fitness_promotion_policy", map[string]any{
		"champion_revision_id": championRevision.ID,
		"scope": map[string]any{
			"eval_suite_id": "release-evals", "eval_suite_version": "v7",
			"task_distribution_id": "retention-calls", "task_distribution_version": "dist-v3",
		},
		"criteria": []any{
			map[string]any{"metric_kind": "quality", "metric_name": "pass_rate", "comparator": "delta_gte", "threshold": 0.02, "protected": true, "minimum_confidence": 0.95},
			map[string]any{"metric_kind": "regression", "metric_name": "safety_failure_rate", "comparator": "challenger_lte", "threshold": 0.05, "protected": true, "minimum_confidence": 0.95},
			map[string]any{"metric_kind": "runtime", "metric_name": "p95_ms", "comparator": "delta_lte", "threshold": -50.0, "minimum_confidence": 0.95},
			map[string]any{"metric_kind": "cost", "metric_name": "cost_per_task", "comparator": "delta_lte", "threshold": 0.0},
		},
		"correlation_id": "m43-policy",
	}, false)
	var policy fitness.PromotionPolicy
	decodeM41Structured(t, policyResult.StructuredContent, &policy)
	if policy.Champion.RevisionID != championRevision.ID || policy.Scope.TaskDistributionVersion != "dist-v3" || len(policy.Criteria) != 4 || !strings.HasPrefix(policy.PolicyRevision, "sha256:") {
		t.Fatalf("promotion policy lost immutable champion/scope/criteria binding: %+v", policy)
	}

	passing := recordM43Fitness(t, ctx, session, challengerRevision.ID, "dist-v3", "passing", strings.Repeat("2", 64), []any{
		m43Metric("quality", "pass_rate", 0.86, "ratio", 0.85, 0.87),
		m43Metric("regression", "safety_failure_rate", 0.02, "ratio", 0.015, 0.025),
		m43Metric("runtime", "p95_ms", 900, "ms", 890, 910),
		map[string]any{"kind": "cost", "name": "cost_per_task", "value": 90.0, "unit": "microunits", "evidence_ref": "artifact:passing-cost.json"},
	})
	passComparison := compareM43(t, ctx, session, policy.ID, champion.ID, passing.ID, false)
	if passComparison.Result.Decision != fitness.DecisionPassesGate {
		t.Fatalf("clearly better challenger decision=%s, want %s: %+v", passComparison.Result.Decision, fitness.DecisionPassesGate, passComparison.Result)
	}
	if !m43HasMetricKind(passing.Metrics, fitness.MetricCost) || !m43HasMetricKind(passing.Metrics, fitness.MetricRuntime) || !m43HasMetricKind(passing.Metrics, fitness.MetricQuality) {
		t.Fatalf("quality/cost/runtime evidence was not retained as separate metrics: %+v", passing.Metrics)
	}

	regressing := recordM43Fitness(t, ctx, session, challengerRevision.ID, "dist-v3", "regressing", strings.Repeat("3", 64), []any{
		m43Metric("quality", "pass_rate", 0.88, "ratio", 0.87, 0.89),
		m43Metric("regression", "safety_failure_rate", 0.08, "ratio", 0.07, 0.09),
		m43Metric("runtime", "p95_ms", 840, "ms", 830, 850),
		map[string]any{"kind": "cost", "name": "cost_per_task", "value": 70.0, "unit": "microunits"},
	})
	failComparison := compareM43(t, ctx, session, policy.ID, champion.ID, regressing.ID, false)
	if failComparison.Result.Decision != fitness.DecisionFailsGate {
		t.Fatalf("protected regression was compensated by quality/cost/runtime gains: %+v", failComparison.Result)
	}
	var protectedFailure bool
	for _, criterion := range failComparison.Result.Criteria {
		if criterion.Protected && criterion.MetricKind == fitness.MetricRegression && criterion.Status == fitness.CriterionFail {
			protectedFailure = true
		}
	}
	if !protectedFailure {
		t.Fatalf("protected regression failure was not preserved at criterion level: %+v", failComparison.Result.Criteria)
	}

	noisy := recordM43Fitness(t, ctx, session, challengerRevision.ID, "dist-v3", "noisy", strings.Repeat("4", 64), []any{
		m43Metric("quality", "pass_rate", 0.83, "ratio", 0.79, 0.87),
		m43Metric("regression", "safety_failure_rate", 0.02, "ratio", 0.015, 0.025),
		m43Metric("runtime", "p95_ms", 900, "ms", 890, 910),
		map[string]any{"kind": "cost", "name": "cost_per_task", "value": 90.0, "unit": "microunits"},
	})
	noisyComparison := compareM43(t, ctx, session, policy.ID, champion.ID, noisy.ID, false)
	if noisyComparison.Result.Decision != fitness.DecisionInconclusive {
		t.Fatalf("small/noisy difference was not inconclusive: %+v", noisyComparison.Result)
	}

	mismatched := recordM43Fitness(t, ctx, session, challengerRevision.ID, "dist-v4", "mismatched", strings.Repeat("5", 64), []any{
		m43Metric("quality", "pass_rate", 0.90, "ratio", 0.89, 0.91),
		m43Metric("regression", "safety_failure_rate", 0.01, "ratio", 0.005, 0.015),
		m43Metric("runtime", "p95_ms", 800, "ms", 790, 810),
		map[string]any{"kind": "cost", "name": "cost_per_task", "value": 60.0, "unit": "microunits"},
	})
	compareM43(t, ctx, session, policy.ID, champion.ID, mismatched.ID, true)

	storedResult := callM41Tool(t, ctx, session, "get_fitness_comparison", map[string]any{"comparison_id": passComparison.ID}, false)
	var stored fitness.Comparison
	decodeM41Structured(t, storedResult.StructuredContent, &stored)
	if stored.Result.Decision != fitness.DecisionPassesGate || stored.Result.PolicyRevision != policy.PolicyRevision || stored.ChampionRevisionID != championRevision.ID || stored.ChallengerRevisionID != challengerRevision.ID {
		t.Fatalf("stored comparison lost deterministic policy/evidence binding: %+v", stored)
	}

	if after := m41CanonicalState(t, ctx, catalog, championRevision.SkillID); after != before {
		t.Fatalf("fitness evidence/comparison changed canonical source/ranking state: before=%+v after=%+v", before, after)
	}
	var evidenceAudits, policyAudits, comparisonAudits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='fitness_evidence_recorded'`).Scan(&evidenceAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='fitness_promotion_policy_created'`).Scan(&policyAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id='demo' AND event_type='fitness_comparison_recorded'`).Scan(&comparisonAudits); err != nil {
		t.Fatal(err)
	}
	if evidenceAudits != 5 || policyAudits != 1 || comparisonAudits != 3 {
		t.Fatalf("fitness audit trace incomplete: evidence=%d policy=%d comparisons=%d", evidenceAudits, policyAudits, comparisonAudits)
	}
}

func recordM43Fitness(t *testing.T, ctx context.Context, session *mcp.ClientSession, revisionID, distributionVersion, runID, sampleDigest string, metrics []any) fitness.Evidence {
	t.Helper()
	result := callM41Tool(t, ctx, session, "record_fitness_evidence", map[string]any{
		"revision_id": revisionID,
		"scope": map[string]any{
			"eval_suite_id": "release-evals", "eval_suite_version": "v7",
			"task_distribution_id": "retention-calls", "task_distribution_version": distributionVersion,
		},
		"executor": map[string]any{"agent": "eval-agent", "model": "gpt-5.6", "harness": "harness@1", "toolchain": "toolchain@1"},
		"run": map[string]any{"run_id": runID, "seed": "42", "sample_count": 500, "sample_set_sha256": sampleDigest},
		"metrics": metrics,
		"provenance": []any{map[string]any{"kind": "report", "reference": "artifact:" + runID + ".json"}},
		"correlation_id": "m43-" + runID,
	}, false)
	var item fitness.Evidence
	decodeM41Structured(t, result.StructuredContent, &item)
	return item
}

func compareM43(t *testing.T, ctx context.Context, session *mcp.ClientSession, policyID, championEvidenceID, challengerEvidenceID string, wantError bool) fitness.Comparison {
	t.Helper()
	result := callM41Tool(t, ctx, session, "compare_fitness_evidence", map[string]any{
		"policy_id": policyID, "champion_evidence_id": championEvidenceID, "challenger_evidence_id": challengerEvidenceID,
	}, wantError)
	if wantError {
		return fitness.Comparison{}
	}
	var item fitness.Comparison
	decodeM41Structured(t, result.StructuredContent, &item)
	return item
}

func m43Metric(kind, name string, value float64, unit string, lower, upper float64) map[string]any {
	return map[string]any{
		"kind": kind, "name": name, "value": value, "unit": unit,
		"uncertainty": map[string]any{"method": "bootstrap", "confidence": 0.95, "lower": lower, "upper": upper},
	}
}

func m43HasMetricKind(metrics []fitness.Metric, kind string) bool {
	for _, metric := range metrics {
		if metric.Kind == kind {
			return true
		}
	}
	return false
}
