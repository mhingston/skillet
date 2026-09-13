// Command skillet-verify runs the deterministic verification gate used by contributors and CI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mhingston/skillet/internal/capabilityeval"
	"github.com/mhingston/skillet/internal/eval"
	"github.com/mhingston/skillet/internal/knowledgeeval"
)

type stepResult struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
}

type metricResult struct {
	Name              string   `json:"name"`
	Fixture           string   `json:"fixture"`
	FixtureVersion    int      `json:"fixture_version"`
	ObservedValue     float64  `json:"observed_value"`
	RequiredThreshold float64  `json:"required_threshold"`
	Comparison        string   `json:"comparison"`
	BaselineValue     *float64 `json:"baseline_value,omitempty"`
	AllowedRegression *float64 `json:"allowed_regression,omitempty"`
	Passed            bool     `json:"passed"`
}

type verificationReport struct {
	SchemaVersion        int            `json:"schema_version"`
	Suite                string         `json:"suite"`
	Passed               bool           `json:"passed"`
	Steps                []stepResult   `json:"steps"`
	Metrics              []metricResult `json:"metrics,omitempty"`
	EvalReport           string         `json:"eval_report"`
	CapabilityEvalReport string         `json:"capability_eval_report,omitempty"`
	KnowledgeEvalReport  string         `json:"knowledge_eval_report,omitempty"`
}

func main() {
	fixturePath := flag.String("fixtures", "evals/retrieval.yaml", "retrieval fixture YAML path")
	baselinePath := flag.String("baseline", "evals/baselines/retrieval-v1.json", "protected retrieval baseline JSON path")
	capabilityFixturePath := flag.String("capability-fixtures", "evals/capabilities.yaml", "scoped capability retrieval fixture YAML path")
	knowledgeFixturePath := flag.String("knowledge-fixtures", "evals/knowledge.yaml", "knowledge retrieval fixture YAML path")
	knowledgeBaselinePath := flag.String("knowledge-baseline", "evals/baselines/knowledge-v1.json", "protected knowledge retrieval baseline JSON path")
	reportDir := flag.String("report-dir", "artifacts/verification", "directory for machine-readable verification reports")
	flag.Parse()

	if err := os.MkdirAll(*reportDir, 0o755); err != nil {
		fatal(fmt.Errorf("create report directory: %w", err))
	}
	rawEvalPath := filepath.Join(*reportDir, "retrieval.json")
	capabilityEvalPath := filepath.Join(*reportDir, "capability-retrieval.json")
	knowledgeEvalPath := filepath.Join(*reportDir, "knowledge-retrieval.json")
	verificationPath := filepath.Join(*reportDir, "verification.json")

	commands := []struct {
		name string
		args []string
	}{
		{name: "go-test", args: []string{"go", "test", "./..."}},
		{name: "go-vet", args: []string{"go", "vet", "./..."}},
		{name: "go-test-race", args: []string{"go", "test", "-race", "./..."}},
		{name: "offline-e2e", args: []string{"go", "test", "./internal/e2e", "-run", "^TestOfflineLocalAdmissionSearchAndMaterialize$", "-count=1"}},
		{name: "offline-scoped-capability-e2e", args: []string{"go", "test", "./internal/e2e", "-run", "^TestOfflineScopedCapabilityDiscoveryAndMaterialization$", "-count=1"}},
		{name: "offline-knowledge-e2e", args: []string{"go", "test", "./internal/e2e", "-run", "^TestOfflineKnowledgeIndexSearchReadAndReindex$", "-count=1"}},
		{name: "retrieval-eval", args: []string{"go", "run", "./cmd/skillet-eval", "--fixtures", *fixturePath, "--baseline", *baselinePath, "--report", rawEvalPath}},
		{name: "capability-retrieval-eval", args: []string{"go", "run", "./cmd/skillet-capability-eval", "--fixtures", *capabilityFixturePath, "--report", capabilityEvalPath}},
		{name: "knowledge-retrieval-eval", args: []string{"go", "run", "./cmd/skillet-knowledge-eval", "--fixtures", *knowledgeFixturePath, "--baseline", *knowledgeBaselinePath, "--report", knowledgeEvalPath}},
	}

	report := verificationReport{
		SchemaVersion: 1, Suite: "vnext-deterministic", Passed: true,
		EvalReport: filepath.ToSlash(rawEvalPath), CapabilityEvalReport: filepath.ToSlash(capabilityEvalPath), KnowledgeEvalReport: filepath.ToSlash(knowledgeEvalPath),
	}
	for _, command := range commands {
		passed := run(command.args)
		report.Steps = append(report.Steps, stepResult{Name: command.name, Command: joinCommand(command.args), Passed: passed})
		if !passed {
			report.Passed = false
		}
	}

	routingMetrics, err := loadMetricResults(*fixturePath, *baselinePath, rawEvalPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillet-verify: routing metric report:", err)
		report.Passed = false
	} else {
		appendMetrics(&report, routingMetrics)
	}

	capabilityMetrics, err := loadCapabilityMetricResults(*capabilityFixturePath, capabilityEvalPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillet-verify: capability metric report:", err)
		report.Passed = false
	} else {
		appendMetrics(&report, capabilityMetrics)
	}

	knowledgeMetrics, err := loadKnowledgeMetricResults(*knowledgeFixturePath, *knowledgeBaselinePath, knowledgeEvalPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillet-verify: knowledge metric report:", err)
		report.Passed = false
	} else {
		appendMetrics(&report, knowledgeMetrics)
	}

	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(fmt.Errorf("encode verification report: %w", err))
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(verificationPath, contents, 0o644); err != nil {
		fatal(fmt.Errorf("write verification report: %w", err))
	}
	fmt.Printf("verification report: %s\n", verificationPath)
	if !report.Passed {
		os.Exit(1)
	}
}

func appendMetrics(report *verificationReport, metrics []metricResult) {
	report.Metrics = append(report.Metrics, metrics...)
	for _, metric := range metrics {
		if !metric.Passed {
			report.Passed = false
		}
	}
}

func run(args []string) bool {
	fmt.Printf("\n==> %s\n", joinCommand(args))
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run() == nil
}

func loadMetricResults(fixturePath, baselinePath, reportPath string) ([]metricResult, error) {
	suite, err := eval.LoadFile(fixturePath)
	if err != nil {
		return nil, err
	}
	observed, err := loadEvalReport(reportPath)
	if err != nil {
		return nil, err
	}
	baseline, err := loadEvalReport(baselinePath)
	if err != nil {
		return nil, err
	}
	maxRegression := suite.Thresholds.MaxRegression
	return []metricResult{
		metric("single_top1_accuracy", suite.Name, suite.Version, observed.Metrics.SingleTop1Accuracy, suite.Thresholds.SingleTop1Accuracy, ">=", baseline.Metrics.SingleTop1Accuracy, maxRegression),
		metric("single_recall_at3", suite.Name, suite.Version, observed.Metrics.SingleRecallAt3, suite.Thresholds.SingleRecallAt3, ">=", baseline.Metrics.SingleRecallAt3, maxRegression),
		metric("multi_recall_at5", suite.Name, suite.Version, observed.Metrics.MultiRecallAt5, suite.Thresholds.MultiRecallAt5, ">=", baseline.Metrics.MultiRecallAt5, maxRegression),
		metric("negative_false_activation_rate", suite.Name, suite.Version, observed.Metrics.NegativeFalseActivationRate, suite.Thresholds.NegativeFalseActivationRate, "<=", baseline.Metrics.NegativeFalseActivationRate, maxRegression),
	}, nil
}

func loadCapabilityMetricResults(fixturePath, reportPath string) ([]metricResult, error) {
	suite, err := capabilityeval.Load(fixturePath)
	if err != nil {
		return nil, err
	}
	observed, err := loadCapabilityEvalReport(reportPath)
	if err != nil {
		return nil, err
	}
	return []metricResult{
		thresholdMetric("capability_top1", suite.Name, suite.Version, observed.Metrics.Top1, suite.Thresholds.Top1, ">="),
		thresholdMetric("capability_recall_at3", suite.Name, suite.Version, observed.Metrics.RecallAt3, suite.Thresholds.RecallAt3, ">="),
		thresholdMetric("capability_multi_recall_at5", suite.Name, suite.Version, observed.Metrics.MultiRecallAt5, suite.Thresholds.MultiRecallAt5, ">="),
		thresholdMetric("capability_negative_false_activation_rate", suite.Name, suite.Version, observed.Metrics.NegativeFalseActivationRate, suite.Thresholds.NegativeFalseActivationRate, "<="),
		thresholdMetric("capability_scope_leakage", suite.Name, suite.Version, observed.Metrics.ScopeLeakage, suite.Thresholds.ScopeLeakage, "<="),
	}, nil
}

func loadKnowledgeMetricResults(fixturePath, baselinePath, reportPath string) ([]metricResult, error) {
	suite, err := knowledgeeval.Load(fixturePath)
	if err != nil {
		return nil, err
	}
	observed, err := loadKnowledgeEvalReport(reportPath)
	if err != nil {
		return nil, err
	}
	baseline, err := loadKnowledgeEvalReport(baselinePath)
	if err != nil {
		return nil, err
	}
	maxRegression := suite.Thresholds.MaxRegression
	return []metricResult{
		metric("knowledge_top1_accuracy", suite.Name, suite.Version, observed.Metrics.Top1Accuracy, suite.Thresholds.Top1Accuracy, ">=", baseline.Metrics.Top1Accuracy, maxRegression),
		metric("knowledge_recall_at3", suite.Name, suite.Version, observed.Metrics.RecallAt3, suite.Thresholds.RecallAt3, ">=", baseline.Metrics.RecallAt3, maxRegression),
		metric("knowledge_mrr", suite.Name, suite.Version, observed.Metrics.MRR, suite.Thresholds.MRR, ">=", baseline.Metrics.MRR, maxRegression),
		metric("knowledge_negative_precision", suite.Name, suite.Version, observed.Metrics.NegativePrecision, suite.Thresholds.NegativePrecision, ">=", baseline.Metrics.NegativePrecision, maxRegression),
	}, nil
}

func thresholdMetric(name, fixture string, fixtureVersion int, observed, threshold float64, comparison string) metricResult {
	passed := observed >= threshold
	if comparison == "<=" {
		passed = observed <= threshold
	}
	return metricResult{
		Name: name, Fixture: fixture, FixtureVersion: fixtureVersion,
		ObservedValue: observed, RequiredThreshold: threshold, Comparison: comparison, Passed: passed,
	}
}

func metric(name, fixture string, fixtureVersion int, observed, threshold float64, comparison string, baseline, maxRegression float64) metricResult {
	passed := observed >= threshold && observed >= baseline-maxRegression
	if comparison == "<=" {
		passed = observed <= threshold && observed <= baseline+maxRegression
	}
	baselineCopy := baseline
	regressionCopy := maxRegression
	return metricResult{
		Name:              name,
		Fixture:           fixture,
		FixtureVersion:    fixtureVersion,
		ObservedValue:     observed,
		RequiredThreshold: threshold,
		Comparison:        comparison,
		BaselineValue:     &baselineCopy,
		AllowedRegression: &regressionCopy,
		Passed:            passed,
	}
}

func loadEvalReport(path string) (eval.Report, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return eval.Report{}, fmt.Errorf("read %s: %w", path, err)
	}
	var report eval.Report
	if err := json.Unmarshal(contents, &report); err != nil {
		return eval.Report{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return report, nil
}

func loadCapabilityEvalReport(path string) (capabilityeval.Report, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return capabilityeval.Report{}, fmt.Errorf("read %s: %w", path, err)
	}
	var report capabilityeval.Report
	if err := json.Unmarshal(contents, &report); err != nil {
		return capabilityeval.Report{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return report, nil
}

func loadKnowledgeEvalReport(path string) (knowledgeeval.Report, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return knowledgeeval.Report{}, fmt.Errorf("read %s: %w", path, err)
	}
	var report knowledgeeval.Report
	if err := json.Unmarshal(contents, &report); err != nil {
		return knowledgeeval.Report{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return report, nil
}

func joinCommand(args []string) string {
	out := ""
	for i, arg := range args {
		if i > 0 {
			out += " "
		}
		out += arg
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "skillet-verify:", err)
	os.Exit(2)
}
