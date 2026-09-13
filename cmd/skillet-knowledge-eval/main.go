package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mhingston/skillet/internal/knowledgeeval"
)

func main() {
	fixture := flag.String("fixtures", "evals/knowledge.yaml", "knowledge evaluation fixture")
	baseline := flag.String("baseline", "", "optional reviewed knowledge baseline")
	reportPath := flag.String("report", "artifacts/verification/knowledge-retrieval.json", "machine-readable report path")
	flag.Parse()

	report, err := knowledgeeval.EvaluateFile(context.Background(), *fixture, *baseline)
	if err != nil {
		fmt.Fprintln(os.Stderr, "knowledge eval:", err)
		os.Exit(2)
	}
	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "knowledge eval:", err)
		os.Exit(2)
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "knowledge eval:", err)
		os.Exit(2)
	}
	if err := os.WriteFile(*reportPath, contents, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "knowledge eval:", err)
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(contents)
	if !report.Passed {
		os.Exit(1)
	}
}
