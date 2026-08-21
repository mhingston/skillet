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
	destination := f.String("destination", "", "host skill directory")
	harness := f.String("harness", "", "pi, claude, or codex")
	_ = f.Parse(args)
	if *candidate == "" || *destination == "" || *harness == "" {
		fail("-candidate, -destination, and -harness are required")
	}
	out, path, err := (adapter.Client{Server: *server, Token: *token}).Materialize(context.Background(), *candidate, filepath.Clean(*destination))
	if err != nil {
		fail(err.Error())
	}
	printJSON(map[string]any{"harness": *harness, "skill": out.Skill, "entrypoint": path, "activation": "materialized", "reload_required": *harness != "pi"})
}

func printJSON(v any)     { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }
func usage()              { fmt.Fprintln(os.Stderr, "usage: skillet-adapter search|materialize ..."); os.Exit(2) }
