package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mhingston/skillet/internal/adapter"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "search":
		search(os.Args[2:])
	case "materialize":
		materialize(os.Args[2:])
	case "lifecycle":
		lifecycle(os.Args[2:])
	case "feedback":
		feedback(os.Args[2:])
	case "feedback-list":
		feedbackList(os.Args[2:])
	default:
		usage()
	}
}

func search(args []string) {
	f := flag.NewFlagSet("search", flag.ExitOnError)
	server := f.String("server", "http://localhost:8080/mcp", "Skillet MCP endpoint")
	token := f.String("token", os.Getenv("SKILLET_TOKEN"), "bearer token")
	query := f.String("query", "", "natural-language query")
	limit := f.Int("limit", 5, "maximum candidates")
	_ = f.Parse(args)
	if *query == "" {
		fail("-query is required")
	}
	out, err := (adapter.Client{Server: *server, Token: *token}).Search(context.Background(), *query, *limit)
	if err != nil {
		fail(err.Error())
	}
	printJSON(out)
}

func materialize(args []string) {
	f := flag.NewFlagSet("materialize", flag.ExitOnError)
	server := f.String("server", "http://localhost:8080/mcp", "Skillet MCP endpoint")
	token := f.String("token", os.Getenv("SKILLET_TOKEN"), "bearer token")
	candidate := f.String("candidate", "", "candidate ID")
	version := f.String("version", "", "exact SemVer")
	versionRange := f.String("range", "", "SemVer range")
	skillID := f.String("skill-id", "", "skill ID for version or range")
	destination := f.String("destination", "", "host skill directory")
	harness := f.String("harness", "", "pi, claude, codex, copilot, or opencode")
	_ = f.Parse(args)
	if (*candidate == "" && *version == "" && *versionRange == "") || (*candidate != "" && (*version != "" || *versionRange != "")) || (*version != "" && *versionRange != "") || ((*version != "" || *versionRange != "") && *skillID == "") || *destination == "" || *harness == "" {
		fail("exactly one of -candidate, -version, or -range plus -destination and -harness are required")
	}
	values := []string{*version, *versionRange, filepath.Clean(*destination)}
	if *version != "" || *versionRange != "" {
		values = []string{*version, *versionRange, *skillID, filepath.Clean(*destination)}
	}
	out, path, err := (adapter.Client{Server: *server, Token: *token}).Materialize(context.Background(), *candidate, values...)
	if err != nil {
		fail(err.Error())
	}
	printJSON(map[string]any{"harness": *harness, "skill": out.Skill, "entrypoint": path, "lifecycle": out.Lifecycle, "activation": "materialized", "reload_required": *harness != "pi"})
}

func lifecycle(args []string) {
	f := flag.NewFlagSet("lifecycle", flag.ExitOnError)
	server := f.String("server", "http://localhost:8080/mcp", "Skillet MCP endpoint")
	token := f.String("token", os.Getenv("SKILLET_TOKEN"), "bearer token")
	reference := f.String("reference", "", "JSON lifecycle reference returned by materialize")
	event := f.String("event", "", "activated, deactivated, completed, or failed")
	source := f.String("source", "", "harness or adapter identifier")
	correlation := f.String("correlation", "", "optional opaque session or run identifier")
	_ = f.Parse(args)
	if *reference == "" || *event == "" {
		fail("-reference and -event are required")
	}
	var ref adapter.LifecycleReference
	if err := json.Unmarshal([]byte(*reference), &ref); err != nil {
		fail("-reference must be lifecycle JSON returned by materialize")
	}
	out, err := (adapter.Client{Server: *server, Token: *token}).ReportLifecycle(context.Background(), ref, *event, *source, *correlation)
	if err != nil {
		fail(err.Error())
	}
	printJSON(out)
}

func feedback(args []string) {
	f := flag.NewFlagSet("feedback", flag.ExitOnError)
	server := f.String("server", "http://localhost:8080/mcp", "Skillet MCP endpoint")
	token := f.String("token", os.Getenv("SKILLET_TOKEN"), "bearer token")
	reference := f.String("reference", "", "JSON lifecycle reference returned by materialize")
	category := f.String("category", "", "bounded feedback category")
	summary := f.String("summary", "", "short factual feedback summary")
	source := f.String("source", "", "harness or adapter identifier")
	correlation := f.String("correlation", "", "optional opaque session or run identifier")
	_ = f.Parse(args)
	if *reference == "" || *category == "" || *summary == "" {
		fail("-reference, -category, and -summary are required")
	}
	var ref adapter.LifecycleReference
	if err := json.Unmarshal([]byte(*reference), &ref); err != nil {
		fail("-reference must be lifecycle JSON returned by materialize")
	}
	out, err := (adapter.Client{Server: *server, Token: *token}).ReportFeedback(context.Background(), ref, *category, *summary, *source, *correlation)
	if err != nil {
		fail(err.Error())
	}
	printJSON(out)
}

func feedbackList(args []string) {
	f := flag.NewFlagSet("feedback-list", flag.ExitOnError)
	server := f.String("server", "http://localhost:8080/mcp", "Skillet MCP endpoint")
	token := f.String("token", os.Getenv("SKILLET_TOKEN"), "bearer token")
	skillID := f.String("skill-id", "", "skill ID")
	revisionID := f.String("revision-id", "", "immutable revision ID")
	category := f.String("category", "", "optional feedback category")
	limit := f.Int("limit", 25, "maximum feedback records")
	offset := f.Int("offset", 0, "records to skip")
	_ = f.Parse(args)
	if *skillID == "" && *revisionID == "" {
		fail("-skill-id or -revision-id is required")
	}
	out, err := (adapter.Client{Server: *server, Token: *token}).ListFeedback(context.Background(), *skillID, *revisionID, *category, *limit, *offset)
	if err != nil {
		fail(err.Error())
	}
	printJSON(out)
}

func printJSON(v any)     { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: skillet-adapter search|materialize|lifecycle|feedback|feedback-list ...")
	os.Exit(2)
}
