package eval

import (
	"errors"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/search"
)

func TestLoadRejectsInvalidFixture(t *testing.T) {
	_, err := Load(strings.NewReader(`
version: 1
name: broken
documents:
  - id: duplicate
    name: one
    description: first
    vector: [1, 0]
  - id: duplicate
    name: two
    description: second
    vector: [0, 1]
cases:
  - id: case-one
    type: single
    query: one
    query_vector: [1, 0]
    relevant_ids: [duplicate]
thresholds:
  single_top1_accuracy: 0.8
  single_recall_at3: 0.95
  multi_recall_at5: 0.9
  negative_false_activation_rate: 0.1
  max_regression: 0.05
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate document id") {
		t.Fatalf("expected duplicate document validation error, got %v", err)
	}
}

func TestEvaluateComputesMetricsAndAppliesGates(t *testing.T) {
	suite := Suite{
		Version: 1,
		Name:    "metric-fixture",
		Documents: []Document{
			{ID: "plan", Name: "plan", Description: "implementation plans", Vector: []float32{1, 0}},
			{ID: "review", Name: "review", Description: "review changes", Vector: []float32{0, 1}},
		},
		Cases: []Case{
			{ID: "single", Type: CaseSingle, Query: "implementation plan", QueryVector: []float32{1, 0}, RelevantIDs: []string{"plan"}},
			{ID: "paraphrase", Type: CaseParaphrase, Query: "roadmap", QueryVector: []float32{1, 0}, RelevantIDs: []string{"plan"}},
			{ID: "multi", Type: CaseMulti, Query: "plan and review", QueryVector: []float32{1, 1}, RelevantIDs: []string{"plan", "review"}},
			{ID: "negative", Type: CaseNegative, Query: "unrelated", QueryVector: []float32{0, 0}},
		},
		Thresholds: Thresholds{
			SingleTop1Accuracy:          1,
			SingleRecallAt3:             1,
			MultiRecallAt5:              1,
			NegativeFalseActivationRate: 0,
			MaxRegression:               0.05,
		},
	}

	idx, err := NewSyntheticIndex(suite)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Evaluate(suite, idx, Config{NegativeActivationScore: 0.02})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("expected passing report: %+v", report)
	}
	if report.Metrics.SingleTop1Accuracy != 1 || report.Metrics.SingleRecallAt3 != 1 || report.Metrics.MultiRecallAt5 != 1 || report.Metrics.NegativeFalseActivationRate != 0 {
		t.Fatalf("unexpected metrics: %+v", report.Metrics)
	}
	if len(report.Cases) != len(suite.Cases) {
		t.Fatalf("case results = %d, want %d", len(report.Cases), len(suite.Cases))
	}
}

func TestEvaluateReportsGateFailureAndRegression(t *testing.T) {
	suite := Suite{
		Version: 1,
		Name:    "gate-fixture",
		Documents: []Document{
			{ID: "plan", Name: "plan", Description: "implementation plans", Vector: []float32{1, 0}},
		},
		Cases: []Case{
			{ID: "single", Type: CaseSingle, Query: "review", QueryVector: []float32{0, 1}, RelevantIDs: []string{"plan"}},
			{ID: "negative", Type: CaseNegative, Query: "unrelated", QueryVector: []float32{1, 0}},
		},
		Thresholds: Thresholds{SingleTop1Accuracy: 1, SingleRecallAt3: 1, NegativeFalseActivationRate: 0, MaxRegression: 0.05},
	}
	idx, err := NewSyntheticIndex(suite)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Evaluate(suite, idx, Config{NegativeActivationScore: 0.01, Baseline: &Report{SchemaVersion: 1, Suite: suite.Name, Metrics: Metrics{SingleTop1Accuracy: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("failed gate unexpectedly passed")
	}
	if len(report.Failures) == 0 {
		t.Fatal("expected threshold or regression failures")
	}
}

func TestEvaluateCountsSearchErrorsAsFailedCases(t *testing.T) {
	suite := Suite{
		Version: 1,
		Name:    "error-fixture",
		Documents: []Document{
			{ID: "plan", Name: "plan", Description: "implementation plans", Vector: []float32{1, 0}},
		},
		Cases:      []Case{{ID: "single", Type: CaseSingle, Query: "implementation plan", QueryVector: []float32{1, 0}, RelevantIDs: []string{"plan"}}},
		Thresholds: Thresholds{SingleTop1Accuracy: 1, SingleRecallAt3: 1},
	}
	report, err := Evaluate(suite, alwaysErrorSearcher{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.Metrics.SingleTop1Accuracy != 0 || report.Metrics.SingleRecallAt3 != 0 {
		t.Fatalf("search error was hidden: passed=%v metrics=%+v failures=%v", report.Passed, report.Metrics, report.Failures)
	}
	if len(report.Cases) != 1 || report.Cases[0].Error == "" {
		t.Fatalf("missing case error evidence: %+v", report.Cases)
	}
}

func TestValidateRejectsConflictingDuplicateQueryVectors(t *testing.T) {
	suite := Suite{
		Version: 1,
		Name:    "duplicate-query-fixture",
		Documents: []Document{
			{ID: "plan", Name: "plan", Description: "implementation plans", Vector: []float32{1, 0}},
		},
		Cases: []Case{
			{ID: "one", Type: CaseSingle, Query: "same query", QueryVector: []float32{1, 0}, RelevantIDs: []string{"plan"}},
			{ID: "two", Type: CaseSingle, Query: "same query", QueryVector: []float32{0, 1}, RelevantIDs: []string{"plan"}},
		},
	}
	if err := suite.Validate(); err == nil || !strings.Contains(err.Error(), "query vector") {
		t.Fatalf("expected conflicting query vector error, got %v", err)
	}
}

func TestLoadAndEvaluateSyntheticFixture(t *testing.T) {
	suite, err := LoadFile("../../evals/retrieval.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireCoverage(suite); err != nil {
		t.Fatal(err)
	}
	idx, err := NewSyntheticIndex(suite)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Evaluate(suite, idx, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("synthetic retrieval fixture failed: %s", strings.Join(report.Failures, "; "))
	}
}

func TestNegativeActivationUsesExplicitEvidenceThreshold(t *testing.T) {
	if activated([]search.Hit{{Score: 0.019, LexicalRank: 0}}, 0.02) {
		t.Fatal("below-threshold dense-only result activated")
	}
	if !activated([]search.Hit{{Score: 0.02, LexicalRank: 0}}, 0.02) {
		t.Fatal("threshold result did not activate")
	}
}

type alwaysErrorSearcher struct{}

func (alwaysErrorSearcher) Search(string, int, int, int, int) ([]search.Hit, bool, error) {
	return nil, false, errors.New("synthetic search failure")
}
