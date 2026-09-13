package knowledgemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestKnowledgeMCPContractsAndBounds(t *testing.T) {
	ctx := context.Background()
	bundle := t.TempDir()
	writeConcept(t, bundle, "target.md", `---
type: Reference
title: Target
---

# Target
unique-target-token commonterm
`)
	for i := 0; i < 60; i++ {
		writeConcept(t, bundle, fmt.Sprintf("source-%02d.md", i), fmt.Sprintf(`---
type: Reference
title: Source %02d
---

# Source
commonterm [target](target.md)
`, i))
	}
	service, err := knowledge.Open(ctx, t.TempDir(), knowledge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "contract", Root: bundle, Revision: "r1"}}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(Handler(service, 1<<20))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("tool count = %d, want 3", len(tools.Tools))
	}
	schemas := map[string]string{}
	for _, tool := range tools.Tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		schemas[tool.Name] = string(encoded)
	}
	if !strings.Contains(schemas["search_knowledge"], `"maximum":10`) {
		t.Fatalf("search_knowledge schema is not capped at 10: %s", schemas["search_knowledge"])
	}
	if !strings.Contains(schemas["get_backlinks"], `"maximum":50`) {
		t.Fatalf("get_backlinks schema is not capped at 50: %s", schemas["get_backlinks"])
	}

	search := callTool(t, ctx, session, "search_knowledge", map[string]any{"query": "commonterm", "limit": 10})
	var searchOut knowledge.OKFSearchResponse
	decodeStructured(t, search.StructuredContent, &searchOut)
	if len(searchOut.Results) != 10 {
		t.Fatalf("search result count = %d, want 10", len(searchOut.Results))
	}

	targetSearch := callTool(t, ctx, session, "search_knowledge", map[string]any{"query": "unique-target-token", "limit": 1})
	var targetOut knowledge.OKFSearchResponse
	decodeStructured(t, targetSearch.StructuredContent, &targetOut)
	if len(targetOut.Results) != 1 {
		t.Fatalf("target result count = %d, want 1", len(targetOut.Results))
	}
	documentID := targetOut.Results[0].Result.DocumentID

	defaultBacklinks := callTool(t, ctx, session, "get_backlinks", map[string]any{"document_id": documentID})
	var defaultOut backlinksOutput
	decodeStructured(t, defaultBacklinks.StructuredContent, &defaultOut)
	if len(defaultOut.Backlinks) != 25 {
		t.Fatalf("default backlink count = %d, want 25", len(defaultOut.Backlinks))
	}
	maxBacklinks := callTool(t, ctx, session, "get_backlinks", map[string]any{"document_id": documentID, "limit": 50})
	var maxOut backlinksOutput
	decodeStructured(t, maxBacklinks.StructuredContent, &maxOut)
	if len(maxOut.Backlinks) != 50 {
		t.Fatalf("max backlink count = %d, want 50", len(maxOut.Backlinks))
	}

	tooMany := callToolAllowError(t, ctx, session, "get_backlinks", map[string]any{"document_id": documentID, "limit": 51})
	if !tooMany.IsError {
		t.Fatal("get_backlinks accepted limit above contract maximum")
	}
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result := callToolAllowError(t, ctx, session, name, arguments)
	if result.IsError {
		t.Fatalf("%s returned tool error: %+v", name, result.Content)
	}
	return result
}

func callToolAllowError(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeStructured(t *testing.T, value any, target any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}

func writeConcept(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
