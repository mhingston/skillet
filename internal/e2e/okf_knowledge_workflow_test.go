package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/knowledgemcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOKFKnowledgeWorkflowThroughStreamableHTTP(t *testing.T) {
	ctx := context.Background()
	bundleRoot := filepath.Join(t.TempDir(), "bundle")
	copyFixtureTree(t, filepath.Join("..", "knowledge", "testdata", "okf-v02"), bundleRoot)
	dataDir := filepath.Join(t.TempDir(), "data")
	service, err := knowledge.Open(ctx, dataDir, knowledge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "retention", Root: bundleRoot, Locator: "git://fixture/retention", Revision: "rev-1"}}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", knowledgemcp.Handler(service, 1<<20))
	server := httptest.NewServer(mux)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "okf-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
		if tool.Name == "search_knowledge" {
			encoded, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(string(encoded), `"maximum":10`) {
				t.Fatalf("search_knowledge schema is not bounded to 10: %s", encoded)
			}
		}
	}
	for _, name := range []string{"search_knowledge", "read_knowledge", "get_backlinks"} {
		if !seen[name] {
			t.Fatalf("missing MCP tool %q", name)
		}
	}

	search := callKnowledgeSearch(t, ctx, session, "renewal objections", 5)
	if len(search.Results) == 0 {
		t.Fatal("search_knowledge returned no results")
	}
	var renewal struct {
		Result   knowledge.Result   `json:"result"`
		Metadata knowledge.Metadata `json:"metadata"`
	}
	foundRenewal := false
	for _, result := range search.Results {
		if result.Metadata.Title == "Renewal objection policy" {
			renewal = result
			foundRenewal = true
			break
		}
	}
	if !foundRenewal {
		t.Fatalf("renewal policy not found: %+v", search.Results)
	}
	if renewal.Result.SourceRevision != "rev-1" || len(renewal.Metadata.Sources) != 1 || renewal.Metadata.Sources[0].ID != "policy-repo" {
		t.Fatalf("provenance not preserved: result=%+v metadata=%+v", renewal.Result, renewal.Metadata)
	}

	read := callKnowledgeRead(t, ctx, session, renewal.Result.ChunkID)
	if read.Metadata.Title != "Renewal objection policy" || read.Chunk.DocumentID != renewal.Result.DocumentID || !strings.Contains(read.Chunk.Content, "member's needs") {
		t.Fatalf("read_knowledge = %+v", read)
	}
	backlinks := callBacklinks(t, ctx, session, renewal.Result.DocumentID)
	if len(backlinks) != 1 || backlinks[0].Path != "overview.md" {
		t.Fatalf("explicit backlinks = %+v", backlinks)
	}

	overview := callKnowledgeSearch(t, ctx, session, "retention coaching overview", 5)
	var oldOverviewChunk string
	for _, result := range overview.Results {
		if result.Metadata.Title == "Retention coaching overview" {
			oldOverviewChunk = result.Result.ChunkID
			readOverview := callKnowledgeRead(t, ctx, session, oldOverviewChunk)
			unresolved := false
			for _, link := range readOverview.Links {
				if link.TargetPath == "missing.md" && !link.Resolved {
					unresolved = true
				}
			}
			if !unresolved {
				t.Fatalf("broken link was not retained as unresolved data: %+v", readOverview.Links)
			}
			break
		}
	}
	if oldOverviewChunk == "" {
		t.Fatal("overview chunk not found")
	}

	glossaryBefore := callKnowledgeSearch(t, ctx, session, "NBA next best action", 5)
	glossaryDocumentID := findDocumentByTitle(t, glossaryBefore, "Retention glossary")

	if err := os.Remove(filepath.Join(bundleRoot, "obsolete.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "new-guidance.md"), []byte(`---
type: Reference
title: Escalation guidance
description: Added during reconciliation.
generated: {by: process:fixture-generator, at: 2026-09-12T12:00:00Z}
status: stable
---

# Escalation

Escalate a policy exception to the policy owner.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "overview.md"), []byte(`---
type: Playbook
title: Retention coaching overview
description: Updated entry point for retention coaching guidance.
sources:
  - id: ops-handbook
    resource: https://example.invalid/ops/retention
generated: {by: process:fixture-generator, at: 2026-09-12T12:00:00Z}
verified: {by: human:reviewer, at: 2026-09-12T13:00:00Z}
status: stable
---

# Purpose

Updated guidance still links to the [renewals policy](/policies/renewals.md).
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "retention", Root: bundleRoot, Locator: "git://fixture/retention", Revision: "rev-2"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.ReadOKF(ctx, oldOverviewChunk); err == nil {
		t.Fatal("stale overview chunk survived reconciliation")
	}
	if got := callKnowledgeSearch(t, ctx, session, "obsolete retention guidance", 10); containsTitle(got, "Obsolete retention guidance") {
		t.Fatalf("deleted document still searchable: %+v", got.Results)
	}
	if got := callKnowledgeSearch(t, ctx, session, "policy exception escalation", 5); !containsTitle(got, "Escalation guidance") {
		t.Fatalf("added document not searchable: %+v", got.Results)
	}
	glossaryAfter := callKnowledgeSearch(t, ctx, session, "NBA next best action", 5)
	if stable := findDocumentByTitle(t, glossaryAfter, "Retention glossary"); stable != glossaryDocumentID {
		t.Fatalf("unchanged document identity changed: before=%s after=%s", glossaryDocumentID, stable)
	}

	if err := os.WriteFile(filepath.Join(bundleRoot, "malformed.md"), []byte("---\ntitle: Missing required type\n---\n\n# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "retention", Root: bundleRoot, Locator: "git://fixture/retention", Revision: "rev-bad"}}); err == nil {
		t.Fatal("malformed OKF update unexpectedly succeeded")
	}
	preserved := callKnowledgeSearch(t, ctx, session, "policy exception escalation", 5)
	if !containsTitle(preserved, "Escalation guidance") || preserved.Results[0].Result.SourceRevision != "rev-2" {
		t.Fatalf("malformed reconciliation corrupted authoritative state: %+v", preserved.Results)
	}
}

type searchEnvelope struct {
	Results []struct {
		Result   knowledge.Result   `json:"result"`
		Metadata knowledge.Metadata `json:"metadata"`
	} `json:"results"`
}

func callKnowledgeSearch(t *testing.T, ctx context.Context, session *mcp.ClientSession, query string, limit int) searchEnvelope {
	t.Helper()
	response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_knowledge", Arguments: map[string]any{"query": query, "limit": limit}})
	if err != nil {
		t.Fatal(err)
	}
	if response.IsError {
		t.Fatalf("search_knowledge returned error: %+v", response.Content)
	}
	var result searchEnvelope
	decodeStructuredContent(t, response.StructuredContent, &result)
	return result
}

func callKnowledgeRead(t *testing.T, ctx context.Context, session *mcp.ClientSession, chunkID string) knowledge.OKFRead {
	t.Helper()
	response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_knowledge", Arguments: map[string]any{"chunk_id": chunkID}})
	if err != nil {
		t.Fatal(err)
	}
	var result knowledge.OKFRead
	decodeStructuredContent(t, response.StructuredContent, &result)
	return result
}

func callBacklinks(t *testing.T, ctx context.Context, session *mcp.ClientSession, documentID string) []knowledge.Backlink {
	t.Helper()
	response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_backlinks", Arguments: map[string]any{"document_id": documentID}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Backlinks []knowledge.Backlink `json:"backlinks"`
	}
	decodeStructuredContent(t, response.StructuredContent, &result)
	return result.Backlinks
}

func decodeStructuredContent(t *testing.T, value any, target any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}

func containsTitle(search searchEnvelope, title string) bool {
	for _, result := range search.Results {
		if result.Metadata.Title == title {
			return true
		}
	}
	return false
}

func findDocumentByTitle(t *testing.T, search searchEnvelope, title string) string {
	t.Helper()
	for _, result := range search.Results {
		if result.Metadata.Title == title {
			return result.Result.DocumentID
		}
	}
	t.Fatalf("title %q not found in %+v", title, search.Results)
	return ""
}

func copyFixtureTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(current)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		t.Fatal(err)
	}
}
