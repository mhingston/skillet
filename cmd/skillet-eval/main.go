// Command skillet-eval runs the deterministic synthetic retrieval suite.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mhingston/skillet/internal/eval"
)

func main() {
	fixturePath := flag.String("fixtures", "evals/retrieval.yaml", "retrieval fixture YAML path")
	reportPath := flag.String("report", "", "optional JSON report output path")
	baselinePath := flag.String("baseline", "", "optional prior JSON report for regression checks")
	flag.Parse()

	suite, err := eval.LoadFile(*fixturePath)
	if err != nil {
		fatal(err)
	}
	if err := eval.RequireCoverage(suite); err != nil {
		fatal(err)
	}
	var baseline *eval.Report
	if *baselinePath != "" {
		contents, err := os.ReadFile(*baselinePath)
		if err != nil {
			fatal(fmt.Errorf("read baseline: %w", err))
		}
		var parsed eval.Report
		if err := json.Unmarshal(contents, &parsed); err != nil {
			fatal(fmt.Errorf("decode baseline: %w", err))
		}
		baseline = &parsed
	}
	index, err := eval.NewSyntheticIndex(suite)
	if err != nil {
		fatal(err)
	}
	report, err := eval.Evaluate(suite, index, eval.Config{Baseline: baseline})
	if err != nil {
		fatal(err)
	}
	contents, err := report.JSON()
	if err != nil {
		fatal(fmt.Errorf("encode report: %w", err))
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(contents, '\n'), 0o644); err != nil {
			fatal(fmt.Errorf("write report: %w", err))
		}
	}
	fmt.Println(string(contents))
	if !report.Passed {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "skillet-eval:", err)
	os.Exit(2)
}
