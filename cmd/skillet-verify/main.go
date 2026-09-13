// Command skillet-verify runs the deterministic verification gate used by contributors and CI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mhingston/skillet/internal/eval"
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
	SchemaVersion int            `json:"schema_version"`
	Suite         string         `json:"suite"`
	Passed        bool           `json:"passed"`
	Steps         []stepResult   `json:"steps"`
	Metrics       []metricResult `json:"metrics,omitempty"`
	EvalReport    string         `json:"eval_report"`
}

func main() {
	fixturePath := flag.String("fixtures", "evals/retrieval.yaml", "retrieval fixture YAML path")
	baselinePath := flag.String("baseline", "evals/baselines/retrieval-v1.json", "protected retrieval baseline JSON path")
	reportDir := flag.String("report-dir", "artifacts/verification", "directory for machine-readable verification reports")
	flag.Parse()

	if err := os.MkdirAll(*reportDir, 0o755); err != nil {
		fatal(fmt.Errorf("create report directory: %w", err))
	}
	rawEvalPath := filepath.Join(*reportDir, "retrieval.json")
	verificationPath := filepath.Join(*reportDir, "verification.json")

	commands := []struct {
		name string
		args []string
	}{
		{name: "go-test", args: []string{"go", "test", "./..."}},
		{name: "go-vet", args: []string{"go", "vet", "./..."}},
		{name: "go-test-race", args: []string{"go", "test", "-race", "./..."}},
		{name: "offline-e2e", args: []string{"go", "test", "./internal/e2e", "-run", "^TestOfflineLocalAdmissionSearchAndMaterialize$", "-count=1"}},
		{name: "retrieval-eval", args: []string{"go", "run", "./cmd/skillet-eval", "--fixtures", *fixturePath, "--baseline", *baselinePath, "--report", rawEvalPath}},
	}

	report := verificationReport{SchemaVersion: 1, Suite: "vnext-deterministic", Passed: true, EvalReport: filepath.ToSlash(rawEvalPath)}
	for _, command := range commands {
		passed := run(command.args)
		report.Steps = append(report.Steps, stepResult{Name: command.name, Command: joinCommand(command.args), Passed: passed})
		if !passed {
			report.Passed = false
		}
	}

	metrics, err := loadMetricResults(*fixturePath, *baselinePath, rawEvalPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillet-verify: metric report:", err)
		report.Passed = false
	} else {
		report.Metrics = metrics
		for _, metric := range metrics {
			if !metric.Passed {
				report.Passed = false
			}
		}
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
		metric("single_top1_accuracy", suite, observed.Metrics.SingleTop1Accuracy, suite.Thresholds.SingleTop1Accuracy, ">=", baseline.Metrics.SingleTop1Accuracy, maxRegression),
		metric("single_recall_at3", suite, observed.Metrics.SingleRecallAt3, suite.Thresholds.SingleRecallAt3, ">=", baseline.Metrics.SingleRecallAt3, maxRegression),
		metric("multi_recall_at5", suite, observed.Metrics.MultiRecallAt5, suite.Thresholds.MultiRecallAt5, ">=", baseline.Metrics.MultiRecallAt5, maxRegression),
		metric("negative_false_activation_rate", suite, observed.Metrics.NegativeFalseActivationRate, suite.Thresholds.NegativeFalseActivationRate, "<=", baseline.Metrics.NegativeFalseActivationRate, maxRegression),
	}, nil
}

func metric(name string, suite eval.Suite, observed, threshold float64, comparison string, baseline, maxRegression float64) metricResult {
	passed := observed >= threshold && observed >= baseline-maxRegression
	if comparison == "<=" {
		passed = observed <= threshold && observed <= baseline+maxRegression
	}
	baselineCopy := baseline
	regressionCopy := maxRegression
	return metricResult{
		Name:              name,
		Fixture:           suite.Name,
		FixtureVersion:    suite.Version,
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
