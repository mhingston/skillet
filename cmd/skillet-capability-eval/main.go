package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mhingston/skillet/internal/capabilityeval"
)

func main() {
	fixture := flag.String("fixtures", "evals/capabilities.yaml", "scoped capability evaluation fixture")
	reportPath := flag.String("report", "artifacts/verification/capability-retrieval.json", "machine-readable report path")
	flag.Parse()

	report, err := capabilityeval.EvaluateFile(*fixture)
	if err != nil {
		fmt.Fprintln(os.Stderr, "capability eval:", err)
		os.Exit(2)
	}
	contents, err := report.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "capability eval:", err)
		os.Exit(2)
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "capability eval:", err)
		os.Exit(2)
	}
	if err := os.WriteFile(*reportPath, contents, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "capability eval:", err)
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(contents)
	if !report.Passed {
		os.Exit(1)
	}
}
