// Package knowledgecli implements local knowledge ingestion/search/read
// operations against the same persisted catalogue served by Skillet MCP.
package knowledgecli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mhingston/skillet/internal/knowledge"
)

// Run executes one knowledge command and writes a single JSON result to stdout.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: skillet-knowledge index|index-okf|search|read ...")
	}
	switch args[0] {
	case "index":
		return runIndex(ctx, args[1:], stdout, stderr)
	case "index-okf":
		return runOKFIndex(ctx, args[1:], stdout, stderr)
	case "search":
		return runSearch(ctx, args[1:], stdout, stderr)
	case "read":
		return runRead(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown skillet-knowledge command %q", args[0])
	}
}

func runIndex(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("index", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "knowledge data directory")
	sourceRoot := flags.String("source-root", "", "local directory or checked-out Git worktree")
	sourceID := flags.String("source-id", "", "stable source identity")
	locator := flags.String("locator", "", "source locator recorded in provenance")
	revision := flags.String("revision", "", "source content revision, such as a Git commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *sourceRoot == "" || *sourceID == "" {
		return errors.New("index requires -data-dir, -source-root, and -source-id")
	}
	service, err := knowledge.Open(ctx, *dataDir, knowledge.Options{})
	if err != nil {
		return err
	}
	defer service.Close()
	stats, err := service.Reindex(ctx, []knowledge.Source{{ID: *sourceID, Root: *sourceRoot, Locator: *locator, Revision: *revision}})
	if err != nil {
		return err
	}
	return writeJSON(stdout, stats)
}

func runOKFIndex(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("index-okf", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "knowledge data directory")
	bundleRoot := flags.String("bundle-root", "", "local OKF bundle directory")
	bundleID := flags.String("bundle-id", "", "stable OKF bundle identity")
	locator := flags.String("locator", "", "source locator recorded in provenance")
	revision := flags.String("revision", "", "source content revision, such as a Git commit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *bundleRoot == "" || *bundleID == "" {
		return errors.New("index-okf requires -data-dir, -bundle-root, and -bundle-id")
	}
	service, err := knowledge.Open(ctx, *dataDir, knowledge.Options{})
	if err != nil {
		return err
	}
	defer service.Close()
	stats, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: *bundleID, Root: *bundleRoot, Locator: *locator, Revision: *revision}})
	if err != nil {
		return err
	}
	return writeJSON(stdout, stats)
}

func runSearch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "knowledge data directory")
	query := flags.String("query", "", "natural-language information need")
	limit := flags.Int("limit", 5, "maximum results")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *query == "" {
		return errors.New("search requires -data-dir and -query")
	}
	service, err := knowledge.Open(ctx, *dataDir, knowledge.Options{})
	if err != nil {
		return err
	}
	defer service.Close()
	result, err := service.Search(ctx, *query, *limit)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runRead(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("read", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", "", "knowledge data directory")
	chunkID := flags.String("chunk-id", "", "stable chunk identity returned by search")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *chunkID == "" {
		return errors.New("read requires -data-dir and -chunk-id")
	}
	service, err := knowledge.Open(ctx, *dataDir, knowledge.Options{})
	if err != nil {
		return err
	}
	defer service.Close()
	chunk, err := service.Read(*chunkID)
	if err != nil {
		return err
	}
	return writeJSON(stdout, chunk)
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
